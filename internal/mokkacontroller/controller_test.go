// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/metadata"
	"github.com/stretchr/testify/require"
)

func TestEventRoutingUsesBoundedDependencyKeys(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)

	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	router.inventoryAdd(inventory)
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups), "inventory work owns configuration materialization")
	require.Equal(t, []statusKey{{kind: statusInventory, name: "inventory", uid: "inventory-uid"}}, drainQueue(queues.status))

	router.profileAdd(&mokkav1alpha1.SGPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile"}})
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups), "inventory work owns profile-driven materialization")

	node := testNode()
	router.nodeAdd(node)
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups))
	require.Empty(t, drainQueue(queues.projections))

	rack := testRack(node)
	require.NoError(t, racks.Add(rack))
	router.rackAdd(rack)
	require.Empty(t, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups), "controller-owned rack adds must not feed back into materialization")
	require.Equal(t, []projectionKey{{mode: projectionApply, rackName: "rack", slotIndex: 0, fresh: true}}, drainQueue(queues.projections))
	require.ElementsMatch(t, []statusKey{
		{kind: statusInventory, name: "inventory", uid: "inventory-uid"},
		{kind: statusRack, name: "rack", uid: "rack-uid"},
	}, drainQueue(queues.status))
}

func TestNoOpUpdatesAreSuppressed(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)

	objects := []struct {
		old any
		new any
		fn  func(any, any)
	}{
		{testInventory(), testInventory().DeepCopy(), router.inventoryUpdate},
		{&mokkav1alpha1.SGPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile"}}, &mokkav1alpha1.SGPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile", ResourceVersion: "2"}}, router.profileUpdate},
		{testRack(testNode()), testRack(testNode()).DeepCopy(), router.rackUpdate},
		{testNode(), testNode().DeepCopy(), router.nodeUpdate},
	}
	for _, object := range objects {
		object.fn(object.old, object.new)
	}
	require.Zero(t, queues.inventories.Len())
	require.Zero(t, queues.groups.Len())
	require.Zero(t, queues.projections.Len())
	require.Zero(t, queues.status.Len())
}

func TestDeleteTombstonesRouteExactCleanupBeforeGroup(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	router := newEventRouter(inventories, racks, registry, queues)
	inventory := testInventory()
	node := testNode()
	rack := testRack(node)
	require.NoError(t, inventories.Add(inventory))
	require.NoError(t, racks.Add(rack))
	registry.replace(inventory)

	router.nodeDelete(cache.DeletedFinalStateUnknown{Key: node.Name, Obj: node})
	cleanup := drainQueue(queues.projections)
	require.Len(t, cleanup, 1)
	require.Equal(t, projectionCleanup, cleanup[0].mode)
	require.Equal(t, node.UID, cleanup[0].cleanup.Binding.Node.UID)
	require.Zero(t, queues.groups.Len(), "the cleanup worker continues the affected group")

	router.rackDelete(cache.DeletedFinalStateUnknown{Key: rack.Name, Obj: rack})
	require.Len(t, drainQueue(queues.projections), 1)
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))

	router.inventoryDelete(cache.DeletedFinalStateUnknown{Key: inventory.Name, Obj: inventory})
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))

	router.profileDelete(cache.DeletedFinalStateUnknown{Key: "profile", Obj: &mokkav1alpha1.SGPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile"}}})
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))
}

func TestProcessNextRateLimitsErrorsAndForgetsSuccess(t *testing.T) {
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.NewTypedItemFastSlowRateLimiter[string](0, 0, 1),
	)
	t.Cleanup(queue.ShutDown)
	queue.Add("key")

	require.True(t, processNext(context.Background(), queue, func(context.Context, string) error {
		return errors.New("retry")
	}))
	require.Equal(t, 1, queue.NumRequeues("key"))
	require.Eventually(t, func() bool { return queue.Len() == 1 }, time.Second, time.Millisecond)
	require.True(t, processNext(context.Background(), queue, func(context.Context, string) error { return nil }))
	require.Zero(t, queue.NumRequeues("key"))
}

func TestRunFailsClosedWhenCacheSyncFails(t *testing.T) {
	controller := newTestController()
	controller.waitForSync = func(context.Context) bool { return false }
	err := controller.Run(context.Background())
	require.ErrorContains(t, err, "cache sync")
	require.False(t, controller.Ready())
	require.True(t, controller.queues.inventories.ShuttingDown())
}

func TestRunCancelsAndWaitsForWorkers(t *testing.T) {
	controller := newTestController()
	controller.waitForSync = func(context.Context) bool { return true }
	started := make(chan struct{})
	finished := make(chan struct{})
	controller.reconcileInventory = func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	require.Eventually(t, controller.Ready, time.Second, time.Millisecond)
	controller.queues.inventories.Add("inventory")
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	select {
	case <-finished:
	default:
		t.Fatal("Run returned before its worker stopped")
	}
	require.False(t, controller.Ready())
}

func TestFilteredNodeListWatchUsesServerSideSelector(t *testing.T) {
	nodes := &recordingNodeAPI{watcher: watch.NewFake()}
	listWatch := newFilteredNodeListWatch(nodes)
	_, err := listWatch.List(metav1.ListOptions{})
	require.NoError(t, err)
	watcher, err := listWatch.Watch(metav1.ListOptions{})
	require.NoError(t, err)
	watcher.Stop()
	require.Equal(t, []string{
		allocate.EligibleNodeLabel + "=true",
		allocate.EligibleNodeLabel + "=true",
	}, nodes.selectors())
}

func TestSingleNodeEventDoesNotListFromAPI(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	registry.replace(testInventory())
	router := newEventRouter(inventories, racks, registry, queues)
	nodes := &recordingNodeAPI{}

	router.nodeAdd(testNode())
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups))
	require.Zero(t, nodes.listCalls(), "event routing must use cached selectors and indexes")
}

func TestProjectedLabelEventsDoNotRouteInvalidPlacement(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	inventory := testInventory()
	inventory.Spec.RackGroups[0].Placement.NodeSelector = &metav1.LabelSelector{MatchLabels: map[string]string{
		metadata.AssignedLabel: "true",
	}}
	registry.replace(inventory)
	router := newEventRouter(inventories, racks, registry, queues)

	oldNode := testNode()
	projected := oldNode.DeepCopy()
	projected.ResourceVersion = "2"
	projected.Labels[metadata.AssignedLabel] = "true"
	projected.Labels[metadata.CliqueLabel] = "fabric.0"
	projected.Annotations = map[string]string{metadata.AssignmentAnnotation: `{}`}
	router.nodeUpdate(oldNode, projected)

	require.Empty(t, drainQueue(queues.groups), "invalid placement must not react to projection-owned label membership")
	require.Empty(t, drainQueue(queues.projections))
	require.Empty(t, drainQueue(queues.status))

	unchangedProjection := projected.DeepCopy()
	unchangedProjection.ResourceVersion = "3"
	router.nodeUpdate(projected, unchangedProjection)
	require.Zero(t, queues.groups.Len())
	require.Zero(t, queues.projections.Len())
	require.Zero(t, queues.status.Len())
}

func TestProjectedMetadataEventDoesNotReapplyExactBinding(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	inventory := testInventory()
	registry.replace(inventory)
	node := testNode()
	rack := testRack(node)
	rack.Spec.Identity.FabricUUID = "fabric"
	rack.Spec.GPUFabric = &mokkav1alpha1.SGPUGPUFabric{}
	require.NoError(t, racks.Add(rack))
	router := newEventRouter(inventories, racks, registry, queues)

	projected := node.DeepCopy()
	projected.ResourceVersion = "2"
	projected.Labels[metadata.AssignedLabel] = "true"
	projected.Labels[metadata.CliqueLabel] = "fabric.0"
	assignment, err := controllerprojection.EncodeAssignment(rack, &rack.Spec.Slots[0])
	require.NoError(t, err)
	projected.Annotations = map[string]string{metadata.AssignmentAnnotation: assignment}
	router.nodeUpdate(node, projected)
	require.Empty(t, drainQueue(queues.groups))
	require.Empty(t, drainQueue(queues.projections), "the successful projection event must not enqueue itself")
	require.Empty(t, drainQueue(queues.status))

	damaged := projected.DeepCopy()
	damaged.ResourceVersion = "3"
	delete(damaged.Labels, metadata.AssignedLabel)
	router.nodeUpdate(projected, damaged)
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups))
	require.Equal(t, []projectionKey{{mode: projectionApply, rackName: rack.Name, slotIndex: 0}}, drainQueue(queues.projections),
		"external removal of "+metadata.AssignedLabel+" must enqueue repair")
}

func newTestController() *Controller {
	return &Controller{
		options:             Options{Workers: 1},
		queues:              newQueues(0),
		waitForSync:         func(context.Context) bool { return true },
		reconcileInventory:  func(context.Context, string) error { return nil },
		reconcileGroup:      func(context.Context, allocate.GroupKey) error { return nil },
		reconcileProjection: func(context.Context, projectionKey) error { return nil },
		reconcileStatus:     func(context.Context, statusKey) error { return nil },
	}
}

func testInventory() *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{Name: "inventory", UID: "inventory-uid", ResourceVersion: "1"},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.SGPURackGroup{{
			ID: "group", Count: 1,
			ProfileRef: corev1.LocalObjectReference{Name: "profile"},
			Placement:  &mokkav1alpha1.SGPUPlacement{NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "a"}}},
		}}},
	}
}

func testNode() *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node", UID: "node-uid", ResourceVersion: "1",
		Labels: map[string]string{allocate.EligibleNodeLabel: "true", "pool": "a"},
	}}
}

func testRack(node *corev1.Node) *mokkav1alpha1.SGPURack {
	controller := true
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rack", UID: "rack-uid", ResourceVersion: "1",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory",
				Name: "inventory", UID: "inventory-uid", Controller: &controller,
			}},
		},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: "inventory", UID: "inventory-uid"},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
			Slots: []mokkav1alpha1.SGPURackSlot{{
				Index: 0, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID},
			}},
		},
	}
}

func testGroupKey() allocate.GroupKey {
	return allocate.GroupKey{InventoryName: "inventory", InventoryUID: "inventory-uid", RackGroup: "group"}
}

func drainQueue[T comparable](queue workqueue.TypedRateLimitingInterface[T]) []T {
	values := make([]T, 0, queue.Len())
	for queue.Len() > 0 {
		value, shutdown := queue.Get()
		if shutdown {
			break
		}
		queue.Done(value)
		queue.Forget(value)
		values = append(values, value)
	}
	return values
}

type recordingNodeAPI struct {
	corev1client.NodeInterface
	mu      sync.Mutex
	opts    []metav1.ListOptions
	watcher watch.Interface
}

func (r *recordingNodeAPI) List(_ context.Context, opts metav1.ListOptions) (*corev1.NodeList, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts = append(r.opts, opts)
	return &corev1.NodeList{}, nil
}

func (r *recordingNodeAPI) Watch(_ context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts = append(r.opts, opts)
	return r.watcher, nil
}

func (r *recordingNodeAPI) selectors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, 0, len(r.opts))
	for _, opts := range r.opts {
		result = append(result, opts.LabelSelector)
	}
	return result
}

func (r *recordingNodeAPI) listCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.opts)
}
