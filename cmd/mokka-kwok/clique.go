// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	applycore "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

const cliqueLabel = domainLabel + ".cliqueID"

var cliqueResource = schema.GroupVersionResource{Group: "resource.nvidia.com", Version: "v1beta1", Resource: "computedomaincliques"}

type daemonReports struct {
	namespace string
	client    dynamic.ResourceInterface
	pods      coreclient.PodInterface
	watch     cache.SharedIndexInformer
}

func newDaemonReports(client dynamic.Interface, kube coreclient.CoreV1Interface, ns string) *daemonReports {
	w := dynamicinformer.NewFilteredDynamicInformer(client, cliqueResource, ns, 0, cache.Indexers{"node": managedCliqueNodes}, nil).Informer()
	return &daemonReports{namespace: ns, client: client.Resource(cliqueResource).Namespace(ns), pods: kube.Pods(ns), watch: w}
}

func cliqueNodes(obj any) ([]string, error) {
	entries, _, err := unstructured.NestedSlice(obj.(*unstructured.Unstructured).Object, "daemons")
	if err != nil {
		return nil, err
	}
	nodes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if fields, ok := entry.(map[string]any); ok {
			if name, ok := fields["nodeName"].(string); ok {
				nodes = append(nodes, name)
			}
		}
	}
	return nodes, nil
}

// Real NVIDIA daemons may share the driver namespace. Only enqueue Nodes
// with an entry that Mokka applied.
func managedCliqueNodes(obj any) ([]string, error) {
	nodes, err := cliqueNodes(obj)
	if err != nil {
		return nil, err
	}
	managers := obj.(*unstructured.Unstructured).GetManagedFields()
	return slices.DeleteFunc(nodes, func(node string) bool {
		manager := reportManager(node)
		return !slices.ContainsFunc(managers, func(f metav1.ManagedFieldsEntry) bool {
			return f.Operation == metav1.ManagedFieldsOperationApply && f.Manager == manager
		})
	}), nil
}

func (d *daemonReports) run(ctx context.Context, enqueue func(string)) error {
	changed := func(obj any) {
		if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tomb.Obj
		}
		names, err := managedCliqueNodes(obj)
		if err == nil {
			for _, n := range names {
				enqueue(n)
			}
		}
	}
	_, err := d.watch.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: changed, DeleteFunc: changed, UpdateFunc: func(old, cur any) { changed(old); changed(cur) }})
	if err != nil {
		return err
	}
	d.watch.RunWithContext(ctx)
	return nil
}

func daemonDomain(p *corev1.Pod, namespace string) string {
	id := p.Labels[domainLabel]
	owner := metav1.GetControllerOfNoCopy(p)
	if id == "" || p.Namespace != namespace || owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "DaemonSet" || owner.Name != "computedomain-daemon-"+id {
		return ""
	}
	return id
}

func daemonReady(p *corev1.Pod) bool {
	return slices.ContainsFunc(p.Status.Conditions, func(c corev1.PodCondition) bool { return c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue })
}

// Only Pods of the driver's domain DaemonSet, with their own reserved daemon device,
// can supply a report. In particular, a workload's channel is a different device.
func (d *domainChannels) daemonPod(ctx context.Context, node *corev1.Node, pods map[types.UID]*corev1.Pod) (*corev1.Pod, error) {
	for _, pod := range pods {
		id := daemonDomain(pod, d.reports.namespace)
		if id == "" || id != node.Labels[domainLabel] || terminalPod(pod) || pod.DeletionTimestamp != nil {
			continue
		}
		allocated, err := d.hasDaemonDevice(ctx, pod)
		if err != nil {
			return nil, err
		}
		if allocated {
			return pod, nil
		}
	}
	return nil, nil
}

func (d *domainChannels) hasDaemonDevice(ctx context.Context, pod *corev1.Pod) (bool, error) {
	for _, use := range podClaimNames(pod) {
		claim, err := d.claims.get(ctx, pod.Spec.NodeName, cache.ObjectName{Namespace: pod.Namespace, Name: use.claim})
		if err != nil {
			return false, err
		}
		if !use.ready(claim, pod) {
			continue
		}
		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver == domainDriver && result.Pool == pod.Spec.NodeName && result.Device == "daemon-0" {
				return true, nil
			}
		}
	}
	return false, nil
}

// The name can be longer than Kubernetes' field manager limit. Use one stable
// manager per Node so an apply cannot prune another daemon's entry or Pod owner.
func reportManager(node string) string {
	return fmt.Sprintf("mokka-kwok-imex-%x", sha256.Sum256([]byte(node)))
}

func (d *daemonReports) sync(ctx context.Context, node string, slot state.NodePayload, pod *corev1.Pod) error {
	if pod == nil {
		return d.withdrawOther(ctx, node, "")
	}
	fabric := slot.Hardware.Fabric
	clique := fmt.Sprintf("%s.%d", fabric.FabricUUID, fabric.CliqueID)
	if pod.Labels[cliqueLabel] != clique {
		label := applycore.Pod(pod.Name, pod.Namespace).WithUID(pod.UID).WithResourceVersion(pod.ResourceVersion).WithLabels(map[string]string{cliqueLabel: clique})
		if _, err := d.pods.Apply(ctx, label, metav1.ApplyOptions{FieldManager: domainManager}); err != nil {
			return err
		}
	}
	if pod.Status.PodIP == "" {
		return d.withdrawOther(ctx, node, "")
	}
	desired := pod.Labels[domainLabel] + "." + clique
	if err := d.publish(ctx, pod, desired, clique, slot.Index); err != nil {
		return err
	}
	return d.withdrawOther(ctx, node, desired)
}

func cliqueObject(ns, name string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion(domainAPI)
	o.SetKind("ComputeDomainClique")
	o.SetNamespace(ns)
	o.SetName(name)
	return o
}

func (d *daemonReports) publish(ctx context.Context, pod *corev1.Pod, name, clique string, index int32) error {
	node := pod.Spec.NodeName
	status := "NotReady"
	if daemonReady(pod) {
		status = "Ready"
	}
	entry := map[string]any{"nodeName": node, "ipAddress": pod.Status.PodIP, "index": int64(index), "cliqueID": clique, "status": status}
	owner := metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID}
	labels := map[string]string{domainLabel: pod.Labels[domainLabel], cliqueLabel: clique}
	prior, exists, err := d.watch.GetStore().GetByKey(d.namespace + "/" + name)
	if err != nil {
		return err
	}
	if exists && ownsCliqueEntry(prior.(*unstructured.Unstructured), node, owner.UID, false) && reportMatches(prior.(*unstructured.Unstructured), entry, labels, owner) {
		return nil
	}
	o := cliqueObject(d.namespace, name)
	o.SetLabels(labels)
	o.SetOwnerReferences([]metav1.OwnerReference{owner})
	o.Object["daemons"] = []any{entry}
	_, err = d.client.Apply(ctx, name, o, metav1.ApplyOptions{FieldManager: reportManager(node)})
	return err
}

// Matching values alone do not establish apply ownership. In particular, the
// last owner on a clique may belong to a real daemon or a replacement Pod.
func ownsCliqueEntry(o *unstructured.Unstructured, node string, uid types.UID, exclusive bool) bool {
	manager := reportManager(node)
	owned := false
	for _, f := range o.GetManagedFields() {
		mine := f.Manager == manager && f.Operation == metav1.ManagedFieldsOperationApply
		if !mine && !exclusive {
			continue
		}
		daemon, owner, err := ownedCliqueFields(f, node, uid)
		if err != nil {
			return false
		}
		if mine {
			owned = daemon && owner
		} else if daemon || owner {
			return false
		}
	}
	return owned
}

func ownedCliqueFields(f metav1.ManagedFieldsEntry, node string, uid types.UID) (bool, bool, error) {
	if f.Subresource != "" || f.FieldsV1 == nil {
		return false, false, nil
	}
	var fields struct {
		Daemons  map[string]json.RawMessage `json:"f:daemons"`
		Metadata struct {
			Owners map[string]json.RawMessage `json:"f:ownerReferences"`
		} `json:"f:metadata"`
	}
	if err := json.Unmarshal(f.FieldsV1.GetRawBytes(), &fields); err != nil {
		return false, false, err
	}
	_, daemon := fields.Daemons[fmt.Sprintf(`k:{"nodeName":%q}`, node)]
	_, owner := fields.Metadata.Owners[fmt.Sprintf(`k:{"uid":%q}`, uid)]
	return daemon, owner, nil
}

func reportMatches(o *unstructured.Unstructured, entry map[string]any, labels map[string]string, owner metav1.OwnerReference) bool {
	if !slices.ContainsFunc(o.GetOwnerReferences(), func(r metav1.OwnerReference) bool { return r.UID == owner.UID }) {
		return false
	}
	if live := o.GetLabels(); live[domainLabel] != labels[domainLabel] || live[cliqueLabel] != labels[cliqueLabel] {
		return false
	}
	items, _, _ := unstructured.NestedSlice(o.Object, "daemons")
	return slices.ContainsFunc(items, func(item any) bool {
		fields, ok := item.(map[string]any)
		return ok && reflect.DeepEqual(fields, entry)
	})
}

func (s *nodeSlices) withdrawReports(ctx context.Context, node string) error {
	if s == nil || s.domains == nil || s.domains.reports == nil {
		return nil
	}
	return s.domains.reports.withdrawOther(ctx, node, "")
}

func daemonChanged(old, cur *corev1.Pod) bool {
	if old == nil || (old.Labels[domainLabel] == "" && cur.Labels[domainLabel] == "") {
		return false
	}
	return old.Status.PodIP != cur.Status.PodIP || daemonReady(old) != daemonReady(cur) ||
		old.DeletionTimestamp.IsZero() != cur.DeletionTimestamp.IsZero() ||
		old.Labels[domainLabel] != cur.Labels[domainLabel] || old.Labels[cliqueLabel] != cur.Labels[cliqueLabel]
}

func (d *daemonReports) withdrawOther(ctx context.Context, node, desired string) error {
	if !d.watch.HasSynced() {
		return errors.New("waiting for ComputeDomainClique cache")
	}
	found, err := d.watch.GetIndexer().ByIndex("node", node)
	if err != nil {
		return err
	}
	for _, item := range found {
		o := item.(*unstructured.Unstructured)
		if o.GetName() == desired {
			continue
		}
		if err := d.withdraw(ctx, o, node); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (d *daemonReports) withdraw(ctx context.Context, o *unstructured.Unstructured, node string) error {
	uid, rv := o.GetUID(), o.GetResourceVersion()
	nodes, err := cliqueNodes(o)
	if err != nil {
		return err
	}
	owners := o.GetOwnerReferences()
	if len(nodes) == 1 && len(owners) == 1 && ownsCliqueEntry(o, node, owners[0].UID, true) {
		return d.client.Delete(ctx, o.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
	}
	// Omitting this manager's entry and owner removes only its fields. Guard
	// against a concurrent Pod replacement or another writer adding a Node.
	empty := cliqueObject(d.namespace, o.GetName())
	empty.SetUID(uid)
	empty.SetResourceVersion(rv)
	_, err = d.client.Apply(ctx, o.GetName(), empty, metav1.ApplyOptions{FieldManager: reportManager(node)})
	return err
}
