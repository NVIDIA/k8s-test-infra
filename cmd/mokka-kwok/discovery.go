// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"fmt"
	"maps"
	"sync"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	applycore "k8s.io/client-go/applyconfigurations/core/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/projection"
)

type labelPublisher struct {
	nodes     coreclient.NodeInterface
	views     *rackViews
	recorder  record.EventRecorder
	racks     cache.SharedIndexInformer
	nodeWatch cache.SharedIndexInformer
	queue     workqueue.TypedRateLimitingInterface[string]
}

func newLabelPublisher(kube kubernetes.Interface, racks cache.SharedIndexInformer) *labelPublisher {
	return &labelPublisher{
		nodes:     kube.CoreV1().Nodes(),
		racks:     racks,
		queue:     workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		nodeWatch: coreinformers.NewNodeInformer(kube, 0, cache.Indexers{}),
	}
}

// Nodes can lose their binding or their last discovery label while the process
// is down. Keep only discovery metadata and the two GPU status fields in the watch cache.
type nodeObservation struct {
	metav1.PartialObjectMetadata
	assignment string
	ownership  string
	gpu        gpuStatus
	kwok       bool
	assigned   bool
}

func observeNode(obj any) (any, error) {
	// A streaming initial list applies the transform in its temporary store
	// and again when it replaces the informer cache.
	if _, compact := obj.(*nodeObservation); compact {
		return obj, nil
	}
	node, ok := obj.(*corev1.Node)
	if !ok {
		return nil, fmt.Errorf("expected Node, got %T", obj)
	}
	n := &nodeObservation{
		PartialObjectMetadata: metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: node.Name, UID: node.UID, ResourceVersion: node.ResourceVersion}},
		assignment:            node.Annotations[metadata.AssignmentAnnotation],
		kwok:                  isKWOK(node.ObjectMeta),
		assigned:              node.Labels[metadata.AssignedLabel] == "true",
		ownership:             appliedNodeFields(node.ManagedFields),
		gpu:                   readGPUStatus(node),
	}
	if n.relevant() {
		n.Labels = make(map[string]string)
		for _, key := range metadata.DiscoveryLabelKeys() {
			if value, exists := node.Labels[key]; exists {
				n.Labels[key] = value
			}
		}
	}
	return n, nil
}

func appliedNodeFields(entries []metav1.ManagedFieldsEntry) string {
	for _, entry := range entries {
		if entry.Manager == fieldManager && entry.Subresource == "" && entry.Operation == metav1.ManagedFieldsOperationApply && entry.FieldsV1 != nil {
			return entry.FieldsV1.GetRawString()
		}
	}
	return ""
}

func (n *nodeObservation) relevant() bool {
	return n.ownership != "" || n.gpu.capacity.owned || n.gpu.allocatable.owned || n.kwok && n.assignment != ""
}

func isKWOK(node metav1.ObjectMeta) bool {
	_, annotated := node.Annotations[allocate.KWOKNodeAnnotation]
	return annotated || node.Labels[allocate.NodeTypeLabel] == string(mokkav1.NodeTypeKWOK)
}

func (p *labelPublisher) enqueue(obj any) {
	n := obj.(*nodeObservation)
	if n.relevant() {
		p.queue.Add(n.Name)
		p.views.pods.nodeChanged(n.Name)
	}
}

func (p *labelPublisher) nodeUpdated(old, current any) {
	a, b := old.(*nodeObservation), current.(*nodeObservation)
	if a.UID != b.UID {
		p.views.forgetNode(a.UID)
	}
	if a.UID != b.UID || a.assignment != b.assignment || a.gpu != b.gpu || a.ownership != b.ownership || a.kwok != b.kwok || a.assigned != b.assigned || !maps.Equal(a.Labels, b.Labels) {
		p.enqueue(b)
	}
}

func (p *labelPublisher) run(ctx context.Context) error {
	defer p.queue.ShutDown()
	if err := p.nodeWatch.SetTransform(observeNode); err != nil {
		return err
	}
	_, err := p.nodeWatch.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: p.enqueue, UpdateFunc: p.nodeUpdated, DeleteFunc: func(obj any) {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		if n, ok := obj.(*nodeObservation); ok {
			p.views.forgetNode(n.UID)
		}
	}})
	if err != nil {
		return err
	}
	var watchers sync.WaitGroup
	watchers.Go(func() { p.nodeWatch.RunWithContext(ctx) })
	defer watchers.Wait()
	if !cache.WaitForCacheSync(ctx.Done(), p.nodeWatch.HasSynced, p.racks.HasSynced) {
		return nil
	}
	stop := context.AfterFunc(ctx, p.queue.ShutDown)
	defer stop()
	for p.processNext(ctx) {
	}
	return nil
}

func (p *labelPublisher) processNext(ctx context.Context) bool {
	name, shutdown := p.queue.Get()
	if shutdown {
		return false
	}
	defer p.queue.Done(name)
	if err := p.reconcile(ctx, name); err != nil {
		zap.L().Warn("Reconcile KWOK node GPU resources", zap.String("node", name), zap.Error(err))
		p.queue.AddRateLimited(name)
	} else {
		p.queue.Forget(name)
	}
	return true
}

func (p *labelPublisher) reconcile(ctx context.Context, name string) error {
	node, err := p.nodes.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	raw := node.Annotations[metadata.AssignmentAnnotation]
	if !isKWOK(node.ObjectMeta) || raw == "" || node.Labels[metadata.AssignedLabel] != "true" {
		// The rack worker removes allocations before it withdraws Node fields.
		racks, err := p.racks.GetIndexer().ByIndex(inventory.RackByNodeUIDIndex, string(node.UID))
		if err != nil || len(racks) != 0 {
			return err
		}
		return p.publishResources(ctx, node, nil)
	}
	desired, err := p.desiredLabels(node)
	if err != nil || desired == nil {
		return err
	}
	return p.publishResources(ctx, node, desired)
}

func (p *labelPublisher) publishResources(ctx context.Context, node *corev1.Node, desired map[string]string) error {
	updated, err := p.publishCapacity(ctx, node, desired[metadata.GPUCountLabel])
	if err != nil {
		return err
	}
	return p.publish(ctx, updated, desired)
}

// Only publish an accepted response for the exact live Node assignment.
// A rack watch without a usable response leaves the previous labels in place.
func (p *labelPublisher) desiredLabels(node *corev1.Node) (map[string]string, error) {
	a, err := assignment.DecodeAssignment(node.Annotations[metadata.AssignmentAnnotation])
	if err != nil {
		return nil, err
	}
	n, ok := p.views.node(node.UID)
	if !ok || n.Node.Name != node.Name || !matchesPublication(n, a) {
		return nil, nil
	}
	return metadata.DiscoveryLabels(len(n.GPUs), n.Hardware)
}

func matchesPublication(n state.NodePayload, a assignment.Assignment) bool {
	return a.Assigned && !n.Releasing && a.NodeUID == n.Node.UID &&
		a.Rack.Name == n.Hardware.Rack.Name && a.Rack.UID == n.Hardware.Rack.UID &&
		a.NodeIndex == n.Index && a.AssignmentRevision == n.AssignmentRevision &&
		n.RackDeliveryGeneration >= a.RackDeliveryGenerationFloor
}

func (p *labelPublisher) publish(ctx context.Context, node *corev1.Node, desired map[string]string) error {
	// SSA requires the complete set of fields we intend to retain in each apply.
	applied := make(map[string]string)
	changed := false
	for _, key := range metadata.DiscoveryLabelKeys() {
		owned := projection.ManagerOwnsLabel(node, fieldManager, key)
		actual := node.Labels[key]
		value, wanted := desired[key]
		keep := wanted && p.shouldApply(node, key, value, owned)
		if keep {
			applied[key] = value
		}
		if owned != keep || keep && actual != value {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	cfg := applycore.Node(node.Name).WithUID(node.UID).WithResourceVersion(node.ResourceVersion).WithLabels(applied)
	_, err := p.nodes.Apply(ctx, cfg, metav1.ApplyOptions{FieldManager: fieldManager})
	return err
}

func (p *labelPublisher) shouldApply(node *corev1.Node, key, desired string, owned bool) bool {
	if len(validation.IsValidLabelValue(desired)) != 0 {
		p.recorder.Eventf(node, corev1.EventTypeWarning, "InvalidDiscoveryLabel", "Cannot publish %s=%q as a Node label", key, desired)
		return false
	}
	actual, exists := node.Labels[key]
	if !exists {
		return true
	}
	if actual == desired {
		return owned
	}
	if owned && !projection.LabelHasOtherOwner(node, fieldManager, key) {
		return true
	}
	p.recorder.Eventf(node, corev1.EventTypeWarning, "DiscoveryLabelConflict", "Keeping existing %s=%q. The rack specifies %q", key, actual, desired)
	return false
}
