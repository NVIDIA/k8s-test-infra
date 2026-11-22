// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	applycore "k8s.io/client-go/applyconfigurations/core/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
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

const (
	productLabel   = metadata.GPUProductLabel
	boundNodeIndex = "boundNode"
)

type productPublisher struct {
	nodes    coreclient.NodeInterface
	profiles mokkaclientv1.SGPURackProfileInterface
	events   coreclient.EventInterface
	recorder record.EventRecorder
	racks    cache.SharedIndexInformer
	labeled  cache.SharedIndexInformer
	queue    workqueue.TypedRateLimitingInterface[string]
	products *lru.Cache
}

func newProductPublisher(kube kubernetes.Interface, mokka mokkaclient.Interface, racks cache.SharedIndexInformer) *productPublisher {
	return &productPublisher{
		nodes:    kube.CoreV1().Nodes(),
		profiles: mokka.MokkaV1alpha1().SGPURackProfiles(),
		events:   kube.CoreV1().Events(metav1.NamespaceAll),
		racks:    racks,
		products: lru.New(256),
		queue:    workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		labeled:  coreinformers.NewFilteredNodeInformer(kube, 0, cache.Indexers{}, func(opts *metav1.ListOptions) { opts.LabelSelector = productLabel }),
	}
}

func boundNodeNames(obj any) ([]string, error) {
	var names []string
	for _, slot := range obj.(*mokkav1.SGPURack).Spec.Nodes {
		if slot.NodeRef != nil {
			names = append(names, slot.NodeRef.Name)
		}
	}
	return names, nil
}

// Watch product labels as well as racks so releases missed during downtime
// and source-label changes can be reconciled. Discard Node status and spec.
type productObservation struct {
	metav1.PartialObjectMetadata
	state productState
}

type productState struct {
	assignment string
	value      string
	owned      bool
	kwok       bool
}

func observeProduct(obj any) (any, error) {
	node := obj.(*corev1.Node)
	return &productObservation{
		PartialObjectMetadata: metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: node.Name, UID: node.UID, ResourceVersion: node.ResourceVersion}},
		state: productState{
			assignment: node.Annotations[metadata.AssignmentAnnotation],
			value:      node.Labels[productLabel],
			owned:      projection.ManagerOwnsLabel(node, fieldManager, productLabel),
			kwok:       isKWOK(node),
		},
	}, nil
}

func isKWOK(node *corev1.Node) bool {
	_, annotated := node.Annotations[allocate.KWOKNodeAnnotation]
	return annotated || node.Labels[allocate.NodeTypeLabel] == string(mokkav1.NodeTypeKWOK)
}

func (p *productPublisher) enqueue(obj any) {
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = t.Obj
	}
	n, ok := obj.(*productObservation)
	if ok && (n.state.owned || n.state.kwok) {
		p.queue.Add(n.Name)
	}
}

func (p *productPublisher) run(ctx context.Context) error {
	defer p.queue.ShutDown()
	if err := p.labeled.SetTransform(observeProduct); err != nil {
		return err
	}
	_, err := p.labeled.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: p.enqueue, DeleteFunc: p.enqueue, UpdateFunc: func(old, current any) {
		a, b := old.(*productObservation), current.(*productObservation)
		if a.UID != b.UID || a.state != b.state {
			p.enqueue(current)
		}
	}})
	if err != nil {
		return err
	}
	b := record.NewBroadcaster(record.WithContext(ctx))
	defer b.Shutdown()
	b.StartRecordingToSink(&coreclient.EventSinkImpl{Interface: p.events})
	p.recorder = b.NewRecorder(scheme.Scheme, corev1.EventSource{Component: fieldManager})
	var watchers sync.WaitGroup
	watchers.Go(func() { p.labeled.RunWithContext(ctx) })
	defer watchers.Wait()
	if !cache.WaitForCacheSync(ctx.Done(), p.labeled.HasSynced, p.racks.HasSynced) {
		return nil
	}
	stop := context.AfterFunc(ctx, p.queue.ShutDown)
	defer stop()
	for p.processNext(ctx) {
	}
	return nil
}

func (p *productPublisher) processNext(ctx context.Context) bool {
	name, shutdown := p.queue.Get()
	if shutdown {
		return false
	}
	defer p.queue.Done(name)
	if err := p.reconcile(ctx, name); err != nil {
		zap.L().Warn("Reconcile KWOK GPU product", zap.String("node", name), zap.Error(err))
		p.queue.AddRateLimited(name)
	} else {
		p.queue.Forget(name)
	}
	return true
}

func (p *productPublisher) reconcile(ctx context.Context, name string) error {
	node, err := p.nodes.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	racks, err := p.racks.GetIndexer().ByIndex(boundNodeIndex, name)
	if err != nil {
		return err
	}
	if !isKWOK(node) {
		return p.publish(ctx, node, "")
	}
	if len(racks) != 0 {
		ref, match, err := projectedProfile(node, racks)
		if err != nil {
			return err
		}
		if match {
			product, err := p.product(ctx, ref)
			if err != nil {
				return err
			}
			return p.publish(ctx, node, product)
		}
		// The rack watch can see a binding before the Node carries its
		// assignment. Unlabeled Nodes are outside the product watch.
		p.queue.AddAfter(name, 2*time.Second)
	}
	if node.Annotations[metadata.AssignmentAnnotation] == "" {
		return p.publish(ctx, node, "")
	}
	// The Node may belong to another shard. Its replica owns publication.
	return nil
}

func projectedProfile(node *corev1.Node, racks []any) (mokkav1.SGPURackProfileReference, bool, error) {
	value := node.Annotations[metadata.AssignmentAnnotation]
	if value == "" || node.Labels[metadata.AssignedLabel] != "true" {
		return mokkav1.SGPURackProfileReference{}, false, nil
	}
	a, err := assignment.DecodeAssignment(value)
	if err != nil {
		return mokkav1.SGPURackProfileReference{}, false, err
	}
	for _, obj := range racks {
		rack := obj.(*mokkav1.SGPURack)
		if rack.UID != a.Rack.UID || rack.Spec.NodeType != mokkav1.NodeTypeKWOK {
			continue
		}
		slot := rack.Spec.NodeByIndex(a.NodeIndex)
		if slot == nil || !slot.BoundTo(node.Name, node.UID) {
			continue
		}
		expected, err := assignment.EncodeAssignment(rack, slot)
		return rack.Spec.ProfileRef, expected == value, err
	}
	return mokkav1.SGPURackProfileReference{}, false, nil
}

func (p *productPublisher) product(ctx context.Context, ref mokkav1.SGPURackProfileReference) (string, error) {
	if value, ok := p.products.Get(ref); ok {
		return value.(string), nil
	}
	profile, err := p.profiles.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if profile.UID != ref.UID || profile.Generation != ref.Generation {
		return "", fmt.Errorf("waiting for rack to reference current profile %s", ref.Name)
	}
	product := strings.ReplaceAll(profile.Spec.Node.GPUs.Model.ProductName, " ", "-")
	p.products.Add(ref, product)
	return product, nil
}

func (p *productPublisher) publish(ctx context.Context, node *corev1.Node, product string) error {
	actual, exists := node.Labels[productLabel]
	owned := projection.ManagerOwnsLabel(node, fieldManager, productLabel)
	if product != "" && len(validation.IsValidLabelValue(product)) != 0 {
		p.recorder.Eventf(node, corev1.EventTypeWarning, "InvalidGPUProduct", "Profile product %q cannot be used as a Node label", product)
		product = ""
	}
	if product == "" && !owned || product != "" && actual == product {
		return nil
	}
	if product != "" && exists && !owned {
		p.recorder.Eventf(node, corev1.EventTypeWarning, "GPUProductConflict", "Keeping existing %s=%q; the rack profile specifies %q", productLabel, actual, product)
		return nil
	}
	return p.applyProduct(ctx, node, product)
}

func (p *productPublisher) applyProduct(ctx context.Context, node *corev1.Node, product string) error {
	cfg := applycore.Node(node.Name).WithUID(node.UID).WithResourceVersion(node.ResourceVersion)
	if product != "" {
		cfg.WithLabels(map[string]string{productLabel: product})
	}
	_, err := p.nodes.Apply(ctx, cfg, metav1.ApplyOptions{FieldManager: fieldManager})
	if apierrors.IsConflict(err) && apierrors.HasStatusCause(err, metav1.CauseTypeFieldManagerConflict) {
		p.recorder.Eventf(node, corev1.EventTypeWarning, "GPUProductConflict", "Another manager owns %s", productLabel)
		return nil
	}
	return err
}
