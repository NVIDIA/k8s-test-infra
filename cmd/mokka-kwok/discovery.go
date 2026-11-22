// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	applycore "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	metaclient "k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/lru"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/projection"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkaclientv1 "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/typed/api/v1alpha1"
)

type labelPublisher struct {
	nodes         coreclient.NodeInterface
	profiles      mokkaclientv1.SGPURackProfileInterface
	events        coreclient.EventInterface
	recorder      record.EventRecorder
	racks         cache.SharedIndexInformer
	nodeWatch     cache.SharedIndexInformer
	queue         workqueue.TypedRateLimitingInterface[string]
	profileLabels *lru.Cache
}

func newLabelPublisher(kube kubernetes.Interface, mokka mokkaclient.Interface, nodes metaclient.Interface, racks cache.SharedIndexInformer) *labelPublisher {
	return &labelPublisher{
		nodes:         kube.CoreV1().Nodes(),
		profiles:      mokka.MokkaV1alpha1().SGPURackProfiles(),
		events:        kube.CoreV1().Events(metav1.NamespaceAll),
		racks:         racks,
		profileLabels: lru.New(256),
		queue:         workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		nodeWatch:     metadatainformer.NewFilteredMetadataInformer(nodes, corev1.SchemeGroupVersion.WithResource("nodes"), metav1.NamespaceAll, 0, cache.Indexers{}, nil).Informer(),
	}
}

// Nodes can lose their binding or their last discovery label while the process
// is down. Watch all Nodes, retaining only the metadata needed to detect changes.
type nodeObservation struct {
	metav1.PartialObjectMetadata
	assignment string
	ownership  string
	kwok       bool
	assigned   bool
}

func observeNode(obj any) (any, error) {
	// A streaming initial list applies the transform in its temporary store
	// and again when it replaces the informer cache.
	if _, compact := obj.(*nodeObservation); compact {
		return obj, nil
	}
	node, ok := obj.(*metav1.PartialObjectMetadata)
	if !ok {
		return nil, fmt.Errorf("expected Node metadata, got %T", obj)
	}
	n := &nodeObservation{
		PartialObjectMetadata: metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: node.Name, UID: node.UID, ResourceVersion: node.ResourceVersion}},
		assignment:            node.Annotations[metadata.AssignmentAnnotation],
		kwok:                  isKWOK(node.ObjectMeta),
		assigned:              node.Labels[metadata.AssignedLabel] == "true",
		ownership:             appliedNodeFields(node.ManagedFields),
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
	return n.ownership != "" || n.kwok && n.assignment != ""
}

func isKWOK(node metav1.ObjectMeta) bool {
	_, annotated := node.Annotations[allocate.KWOKNodeAnnotation]
	return annotated || node.Labels[allocate.NodeTypeLabel] == string(mokkav1.NodeTypeKWOK)
}

func (p *labelPublisher) enqueue(obj any) {
	n := obj.(*nodeObservation)
	if n.relevant() {
		p.queue.Add(n.Name)
	}
}

func (p *labelPublisher) nodeUpdated(old, current any) {
	a, b := old.(*nodeObservation), current.(*nodeObservation)
	if a.UID != b.UID || a.assignment != b.assignment || a.ownership != b.ownership || a.kwok != b.kwok || a.assigned != b.assigned || !maps.Equal(a.Labels, b.Labels) {
		p.enqueue(b)
	}
}

func (p *labelPublisher) run(ctx context.Context) error {
	defer p.queue.ShutDown()
	if err := p.nodeWatch.SetTransform(observeNode); err != nil {
		return err
	}
	_, err := p.nodeWatch.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: p.enqueue, UpdateFunc: p.nodeUpdated})
	if err != nil {
		return err
	}
	b := record.NewBroadcaster(record.WithContext(ctx))
	defer b.Shutdown()
	b.StartRecordingToSink(&coreclient.EventSinkImpl{Interface: p.events})
	p.recorder = b.NewRecorder(scheme.Scheme, corev1.EventSource{Component: fieldManager})
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
		zap.L().Warn("Reconcile KWOK discovery labels", zap.String("node", name), zap.Error(err))
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
		return p.publish(ctx, node, nil)
	}
	ref, err := p.boundProfile(node)
	if err != nil || ref == nil {
		return err
	}
	desired, err := p.labelsForProfile(ctx, *ref)
	if err != nil {
		return err
	}
	return p.publish(ctx, node, desired)
}

// Only the shard that holds the exact projected rack and slot may publish.
func (p *labelPublisher) boundProfile(node *corev1.Node) (*mokkav1.SGPURackProfileReference, error) {
	raw := node.Annotations[metadata.AssignmentAnnotation]
	a, err := assignment.DecodeAssignment(raw)
	if err != nil || !a.Assigned {
		return nil, err
	}
	obj, exists, err := p.racks.GetStore().GetByKey(a.Rack.Name)
	if err != nil || !exists {
		return nil, err
	}
	rack := obj.(*mokkav1.SGPURack)
	slot := rack.Spec.NodeByIndex(a.NodeIndex)
	if rack.Spec.NodeType != mokkav1.NodeTypeKWOK || slot == nil || !slot.BoundTo(node.Name, node.UID) {
		return nil, nil
	}
	if !assignment.PublishedAt(raw, rack, slot) {
		return nil, nil
	}
	return &rack.Spec.ProfileRef, nil
}

func (p *labelPublisher) labelsForProfile(ctx context.Context, ref mokkav1.SGPURackProfileReference) (map[string]string, error) {
	if value, ok := p.profileLabels.Get(ref); ok {
		return value.(map[string]string), nil
	}
	profile, err := p.profiles.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if profile.UID != ref.UID || profile.Generation != ref.Generation {
		return nil, fmt.Errorf("waiting for rack to reference current profile %s", ref.Name)
	}
	desired := metadata.DiscoveryLabels(profile.Spec.Node.GPUs)
	p.profileLabels.Add(ref, desired)
	return desired, nil
}

func (p *labelPublisher) publish(ctx context.Context, node *corev1.Node, desired map[string]string) error {
	// SSA requires the complete set of fields we intend to retain in each apply.
	applied := make(map[string]string)
	changed := false
	for _, key := range metadata.DiscoveryLabelKeys() {
		owned := projection.ManagerOwnsLabel(node, fieldManager, key)
		actual := node.Labels[key]
		value := desired[key]
		keep := p.shouldApply(node, key, value, owned)
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
	if desired == "" {
		return false
	}
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
