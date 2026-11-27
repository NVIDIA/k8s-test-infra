// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	resourceinformers "k8s.io/client-go/informers/resource/v1"
	"k8s.io/client-go/kubernetes"
	resourceclient "k8s.io/client-go/kubernetes/typed/resource/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

const gpuSliceKey = "mokka.nvidia.com/kwok-gpu-slice"

type nodeSlices struct {
	domains *domainChannels
	// A release must not acknowledge an empty pool while a publication for the
	// previous assignment is between its live Node check and its slice write.
	mu     sync.Mutex
	client resourceclient.ResourceSliceInterface
	driver string
}

func sliceName(uid types.UID) string { return "mokka-kwok-gpu-" + string(uid) }

func ownsSlice(slice *resourcev1.ResourceSlice, nodeName string, uid types.UID) bool {
	owner := metav1.GetControllerOfNoCopy(slice)
	return owner != nil && owner.APIVersion == "v1" && owner.Kind == "Node" && owner.Name == nodeName && owner.UID == uid
}

func (s *nodeSlices) publish(ctx context.Context, node *corev1.Node, slot state.NodePayload) error {
	devices, err := sliceDevices(slot)
	if err != nil {
		return err
	}
	return s.writePool(ctx, node, sliceName(node.UID), gpuSliceKey, s.driver, devices)
}

func (s *nodeSlices) writePool(ctx context.Context, node *corev1.Node, name, key, driver string, devices []resourcev1.Device) error {
	spec := resourcev1.ResourceSliceSpec{Driver: driver, NodeName: &node.Name,
		Pool: resourcev1.ResourcePool{Name: node.Name, Generation: 1, ResourceSliceCount: 1}, Devices: devices}
	current, err := s.client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = s.client.Create(ctx, &resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{
			Name: name, Labels: map[string]string{key: "true"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: node.Name, UID: node.UID, Controller: ptr.To(true)}},
		}, Spec: spec}, metav1.CreateOptions{FieldManager: fieldManager})
		return err
	}
	if err != nil {
		return err
	}
	if !ownsSlice(current, node.Name, node.UID) {
		return fmt.Errorf("slice %s belongs to a different owner", name)
	}
	if current.DeletionTimestamp != nil {
		return fmt.Errorf("waiting for slice %s to be deleted", name)
	}
	if current.Spec.Driver != driver {
		// spec.driver is immutable. A new driver has a different pool.
		if err := s.removeNamed(ctx, name, node.Name, node.UID); err != nil {
			return err
		}
		return fmt.Errorf("replacing slice %s for driver %s", name, driver)
	}
	return s.update(ctx, current, spec, key)
}

func (s *nodeSlices) update(ctx context.Context, current *resourcev1.ResourceSlice, spec resourcev1.ResourceSliceSpec, key string) error {
	spec.Pool.Generation = current.Spec.Pool.Generation
	if !apiequality.Semantic.DeepEqual(current.Spec, spec) {
		spec.Pool.Generation++
	} else if current.Labels[key] == "true" {
		return nil
	}
	current.Spec = spec
	if current.Labels == nil {
		current.Labels = make(map[string]string)
	}
	current.Labels[key] = "true"
	_, err := s.client.Update(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
	return err
}

func (s *nodeSlices) remove(ctx context.Context, node string, uid types.UID) error {
	var gpu, domain error
	if s.driver != "" {
		gpu = s.removeNamed(ctx, sliceName(uid), node, uid)
	}
	if s.domains != nil {
		domain = s.removeNamed(ctx, domainSliceName(uid), node, uid)
	}
	return errors.Join(gpu, domain)
}

func (s *nodeSlices) removeNamed(ctx context.Context, name, node string, uid types.UID) error {
	slice, err := s.client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil || !ownsSlice(slice, node, uid) {
		return err
	}
	if slice.DeletionTimestamp == nil {
		err = s.client.Delete(ctx, slice.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{
			UID: &slice.UID, ResourceVersion: &slice.ResourceVersion,
		}})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	// DELETE may only set deletionTimestamp. Release can proceed on the
	// next reconciliation once GET confirms that finalizers have finished.
	return fmt.Errorf("waiting for deletion of slice %s", slice.Name)
}

// Mark before publishing. A restart in device-plugin mode must never expose
// capacity on top of a pool whose deletion has not been confirmed.
func (p *labelPublisher) markSlice(ctx context.Context, node *corev1.Node, key, name string) (*corev1.Node, error) {
	if node.Annotations[key] == name {
		return node, nil
	}
	var value *string
	if name != "" {
		value = &name
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"uid": node.UID, "resourceVersion": node.ResourceVersion, "annotations": map[string]*string{key: value},
	}})
	if err != nil {
		return nil, err
	}
	return p.nodes.Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
}

func (p *labelPublisher) watchSlices(ctx context.Context, kube kubernetes.Interface, key string) error {
	w := resourceinformers.NewFilteredResourceSliceInformer(kube, 0, cache.Indexers{}, func(o *metav1.ListOptions) {
		o.LabelSelector = key + "=true"
	})
	// Reconcile from live data. Keep only the Node reference in the watch cache.
	if err := w.SetTransform(func(obj any) (any, error) {
		if s, ok := obj.(*resourcev1.ResourceSlice); ok {
			return &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: s.Name, UID: s.UID, ResourceVersion: s.ResourceVersion, OwnerReferences: s.OwnerReferences}}, nil
		}
		return obj, nil
	}); err != nil {
		return err
	}
	enqueue := func(obj any) {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		m, ok := obj.(metav1.Object)
		if !ok {
			return
		}
		if owner := metav1.GetControllerOfNoCopy(m); owner != nil && owner.Kind == "Node" && owner.APIVersion == "v1" {
			p.queue.Add(owner.Name)
		}
	}
	_, err := w.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: enqueue, UpdateFunc: func(old, current any) { enqueue(old); enqueue(current) }, DeleteFunc: enqueue})
	if err != nil {
		return err
	}
	w.RunWithContext(ctx)
	return nil
}
