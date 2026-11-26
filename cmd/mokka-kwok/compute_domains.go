// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	applycore "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/projection"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

const (
	domainDriver   = "compute-domain.nvidia.com"
	domainLabel    = "resource.nvidia.com/computeDomain"
	domainSliceKey = "mokka.nvidia.com/kwok-compute-domain-slice"
	domainManager  = "mokka-kwok-compute-domain"
	domainAPI      = "resource.nvidia.com/v1beta1"
)

type domainChannels struct {
	retry  func(string, time.Duration)
	claims *shardClaims
	watch  cache.SharedIndexInformer
}

func newDomainChannels(client metadata.Interface, claims *shardClaims) *domainChannels {
	gvr := schema.GroupVersionResource{Group: "resource.nvidia.com", Version: "v1beta1", Resource: "computedomains"}
	index := func(obj any) ([]string, error) {
		m := obj.(metav1.Object)
		return []string{m.GetNamespace() + "/" + string(m.GetUID())}, nil
	}
	return &domainChannels{claims: claims, watch: metadatainformer.NewFilteredMetadataInformer(client, gvr, metav1.NamespaceAll, 0, cache.Indexers{domainLabel: index}, nil).Informer()}
}

func (p *labelPublisher) watchDomains(ctx context.Context) error {
	w := p.slices.domains.watch
	_, err := w.AddEventHandler(cache.ResourceEventHandlerFuncs{DeleteFunc: func(obj any) {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		if m, ok := obj.(metav1.Object); ok {
			nodes, err := p.nodeWatch.GetIndexer().IndexKeys(domainLabel, string(m.GetUID()))
			if err != nil {
				utilruntime.HandleErrorWithContext(ctx, err, "Find Nodes for deleted ComputeDomain")
				return
			}
			for _, name := range nodes {
				p.queue.Add(name)
			}
		}
	}})
	if err != nil {
		return err
	}
	w.RunWithContext(ctx)
	return nil
}

func domainSliceName(uid types.UID) string { return "mokka-kwok-domain-" + string(uid) }

func domainDevices() []resourcev1.Device {
	devices := make([]resourcev1.Device, 0, 2)
	for _, kind := range []string{"channel", "daemon"} {
		devices = append(devices, resourcev1.Device{Name: kind + "-0", Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
			"type": {StringValue: ptr.To(kind)}, "id": {IntValue: ptr.To(int64(0))},
		}})
	}
	return devices
}

func (p *labelPublisher) publishDomain(ctx context.Context, node *corev1.Node, slot state.NodePayload) error {
	name := domainSliceName(node.UID)
	if slot.Hardware.Fabric == nil || slot.Hardware.Fabric.Type != "NVLink" {
		if err := p.slices.removeNamed(ctx, name, node.Name, node.UID); err != nil {
			return err
		}
		_, err := p.clearDomain(ctx, node)
		return err
	}
	node, err := p.markSlice(ctx, node, domainSliceKey, name)
	if err != nil {
		return err
	}
	if err := p.slices.writePool(ctx, node, name, domainSliceKey, domainDriver, domainDevices()); err != nil {
		return err
	}
	return p.prepareChannels(ctx, node)
}

func (p *labelPublisher) prepareChannels(ctx context.Context, node *corev1.Node) error {
	d := p.slices.domains
	pods := d.claims.pods.allocationSnapshot(node.Name)
	if pods == nil || !d.claims.hasSynced() || !d.watch.HasSynced() {
		p.queue.AddAfter(node.Name, time.Second)
		return nil
	}
	return p.preparePods(ctx, node, pods)
}

func (p *labelPublisher) preparePods(ctx context.Context, node *corev1.Node, pods map[types.UID]*corev1.Pod) error {
	holders := sets.New[string]()
	for _, pod := range pods {
		if terminalPod(pod) {
			continue
		}
		if err := p.slices.domains.addHolders(ctx, pod, holders); err != nil {
			return err
		}
	}
	current := node.Labels[domainLabel]
	if holders.Has(current) {
		return nil
	}
	var desired string
	if len(holders) > 0 {
		desired = sets.List(holders)[0]
	}
	_, err := p.applyDomainLabel(ctx, node, desired)
	return err
}

func (d *domainChannels) addHolders(ctx context.Context, pod *corev1.Pod, holders sets.Set[string]) error {
	for _, use := range podClaimNames(pod) {
		claim, err := d.claims.get(ctx, pod.Spec.NodeName, cache.ObjectName{Namespace: pod.Namespace, Name: use.claim})
		if err != nil {
			return err
		}
		if !use.ready(claim, pod) {
			continue
		}
		if err := d.addClaim(pod.Spec.NodeName, claim, holders); err != nil {
			return err
		}
	}
	return nil
}

func (d *domainChannels) addClaim(node string, claim *resourcev1.ResourceClaim, holders sets.Set[string]) error {
	for _, r := range claim.Status.Allocation.Devices.Results {
		if r.Driver != domainDriver || r.Pool != node || r.Device != "channel-0" {
			continue
		}
		id := channelDomain(claim.Status.Allocation.Devices.Config, r.Request)
		if id == "" {
			continue
		}
		// Match the real plugin's namespace check. The UID in opaque claim
		// configuration alone does not establish which namespace owns a domain.
		known, err := d.watch.GetIndexer().ByIndex(domainLabel, claim.Namespace+"/"+id)
		if err != nil {
			return err
		}
		if len(known) > 0 {
			holders.Insert(id)
		} else if d.retry != nil {
			d.retry(node, 10*time.Second)
		}
	}
	return nil
}

// The NVIDIA plugin takes the last matching claim config, falling back to
// the last matching DeviceClass config. Daemon configs do not prepare channels.
func channelDomain(configs []resourcev1.DeviceAllocationConfiguration, request string) string {
	base, _, _ := strings.Cut(request, "/")
	for _, source := range []resourcev1.AllocationConfigSource{resourcev1.AllocationConfigSourceClaim, resourcev1.AllocationConfigSourceClass} {
		for i := len(configs) - 1; i >= 0; i-- {
			c := configs[i]
			if c.Source != source || c.Opaque == nil || c.Opaque.Driver != domainDriver {
				continue
			}
			if len(c.Requests) > 0 && !slices.Contains(c.Requests, base) && !slices.Contains(c.Requests, request) {
				continue
			}
			if id := decodeChannelID(c.Opaque.Parameters.Raw); id != "" {
				return id
			}
		}
	}
	return ""
}

func decodeChannelID(raw []byte) string {
	var config struct {
		metav1.TypeMeta
		DomainID string `json:"domainID"`
	}
	if json.Unmarshal(raw, &config) != nil || config.APIVersion != domainAPI || config.Kind != "ComputeDomainChannelConfig" {
		return ""
	}
	return config.DomainID
}

func (p *labelPublisher) applyDomainLabel(ctx context.Context, node *corev1.Node, id string) (*corev1.Node, error) {
	old := node.Labels[domainLabel]
	owned := projection.ManagerOwnsLabel(node, domainManager, domainLabel)
	if old == id || old != "" && !owned {
		return node, nil
	}
	cfg := applycore.Node(node.Name).WithUID(node.UID).WithResourceVersion(node.ResourceVersion)
	if id != "" {
		cfg.WithLabels(map[string]string{domainLabel: id})
	}
	return p.nodes.Apply(ctx, cfg, metav1.ApplyOptions{FieldManager: domainManager})
}

func (p *labelPublisher) clearDomain(ctx context.Context, node *corev1.Node) (*corev1.Node, error) {
	n, err := p.applyDomainLabel(ctx, node, "")
	if err != nil {
		return nil, err
	}
	return p.markSlice(ctx, n, domainSliceKey, "")
}

func (p *labelPublisher) withdrawPools(ctx context.Context, node *corev1.Node) (*corev1.Node, error) {
	if node.Annotations[domainSliceKey] != "" && (p.slices == nil || p.slices.domains == nil) {
		return nil, fmt.Errorf("release the ComputeDomain slice on %s with computeDomains enabled", node.Name)
	}
	if p.slices == nil {
		return node, nil
	}
	if err := p.slices.remove(ctx, node.Name, node.UID); err != nil {
		return nil, err
	}
	var err error
	if p.slices.domains != nil {
		node, err = p.clearDomain(ctx, node)
		if err != nil {
			return nil, err
		}
	}
	if p.dra {
		return p.markSlice(ctx, node, gpuSliceKey, "")
	}
	return node, nil
}
