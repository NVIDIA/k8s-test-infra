// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/metadata"
	"github.com/stretchr/testify/require"
)

func TestEventRoutingUsesBoundedDependencyKeys(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)

	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	router.inventoryAdd(inventory)
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups), "inventory work owns configuration materialization")
	require.Equal(t, []statusKey{{kind: statusInventory, name: "inventory", uid: "inventory-uid"}}, drainQueue(queues.status))

	router.profileAdd(&mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile"}})
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
	require.Equal(t, []projectionKey{{mode: projectionApply, rackName: "rack", nodeIndex: 0, fresh: true}}, drainQueue(queues.projections))
	require.ElementsMatch(t, []statusKey{
		{kind: statusInventory, name: "inventory", uid: "inventory-uid"},
		{kind: statusRack, name: "rack", uid: "rack-uid"},
	}, drainQueue(queues.status))
}

func TestControllerOwnedFreeRackAddContinuesPendingAllocation(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	registry.replace(inventory)
	router := newEventRouter(inventories, racks, registry, queues)

	replacement := testNode()
	replacement.UID = "replacement-uid"
	router.nodeAdd(replacement)
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups),
		"the first reconcile can still observe the deleted rack's stale binding")

	recreated := testRack(replacement)
	recreated.Spec.Nodes[0].NodeRef = nil
	router.rackAdd(recreated)
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups),
		"observing the recreated free slot must reconsider a replacement left pending against stale cache state")
}

func TestNoOpUpdatesAreSuppressed(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)

	objects := []struct {
		old any
		new any
		fn  func(any, any)
	}{
		{testInventory(), testInventory().DeepCopy(), router.inventoryUpdate},
		{&mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile"}}, &mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile", ResourceVersion: "2"}}, router.profileUpdate},
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

func TestAllocationRevisionIgnoresOwnedMetadataAndTracksTopologyInputs(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	var invalidations atomic.Int64
	router := newEventRouter(
		inventories, racks, newPlacementRegistry(), queues,
		func() { invalidations.Add(1) },
	)

	inventory := testInventory()
	finalized := inventory.DeepCopy()
	finalized.Finalizers = []string{controllerack.InventoryFinalizer}
	router.inventoryUpdate(inventory, finalized)
	require.Zero(t, invalidations.Load())

	resized := finalized.DeepCopy()
	resized.Spec.RackGroups[0].Count++
	router.inventoryUpdate(finalized, resized)
	require.EqualValues(t, 1, invalidations.Load())

	profile := &mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile", UID: "profile-uid"}}
	profileRV := profile.DeepCopy()
	profileRV.ResourceVersion = "2"
	router.profileUpdate(profile, profileRV)
	require.EqualValues(t, 1, invalidations.Load())
	profileSpec := profileRV.DeepCopy()
	profileSpec.Spec.Rack.NodesPerRack = 2
	router.profileUpdate(profileRV, profileSpec)
	require.EqualValues(t, 2, invalidations.Load())

	rack := testRack(testNode())
	finalizedRack := rack.DeepCopy()
	finalizedRack.Finalizers = []string{controllerack.RackFinalizer}
	router.rackUpdate(rack, finalizedRack)
	require.EqualValues(t, 2, invalidations.Load())
	rebound := finalizedRack.DeepCopy()
	rebound.Spec.Nodes[0].NodeRef.UID = "replacement-uid"
	router.rackUpdate(finalizedRack, rebound)
	require.EqualValues(t, 3, invalidations.Load())
}

func TestDeleteTombstonesRouteExactCleanupBeforeGroup(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
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

	router.profileDelete(cache.DeletedFinalStateUnknown{Key: "profile", Obj: &mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile"}}})
	require.Equal(t, []string{"inventory"}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))
}

func TestForeignRackDeleteRoutesDesiredNameClaimant(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	router.inventoryAdd(inventory)
	drainQueue(queues.inventories)
	drainQueue(queues.status)

	blocker := &mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{
		Name: materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		UID:  "foreign-rack-uid",
	}}
	require.NoError(t, racks.Add(blocker))
	router.rackAdd(blocker)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	require.NoError(t, racks.Delete(blocker))

	router.rackDelete(cache.DeletedFinalStateUnknown{Key: blocker.Name, Obj: blocker})

	require.Equal(t, []string{inventory.Name}, drainQueue(queues.inventories))
	require.Equal(t, []statusKey{{kind: statusInventory, name: inventory.Name, uid: inventory.UID}}, drainQueue(queues.status))
}

func TestRackOwnershipTransitionRoutesDesiredNameClaimant(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	router.inventoryAdd(inventory)
	drainQueue(queues.inventories)
	drainQueue(queues.status)

	blocker := &mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{
		Name: materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		UID:  "foreign-rack-uid",
	}}
	transitioned := blocker.DeepCopy()
	transitioned.ResourceVersion = "2"
	transitioned.Spec.InventoryRef = mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID}
	transitioned.Spec.Identity = mokkav1alpha1.SGPURackIdentity{RackGroup: "group"}
	controller := true
	transitioned.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory",
		Name: inventory.Name, UID: inventory.UID, Controller: &controller,
	}}

	router.rackUpdate(blocker, transitioned)

	require.Equal(t, []string{inventory.Name}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups), "template and ownership drift is owned by full Inventory reconciliation")
	drainQueue(queues.status)

	released := blocker.DeepCopy()
	released.ResourceVersion = "3"
	router.rackUpdate(transitioned, released)
	require.Equal(t, []string{inventory.Name}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))
}

func TestRackUpdateRoutesBindingsLocallyAndTemplateDriftGlobally(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)

	rack := testRack(testNode())
	rebound := rack.DeepCopy()
	rebound.ResourceVersion = "2"
	rebound.Spec.Nodes[0].NodeRef.UID = "replacement-uid"
	router.rackUpdate(rack, rebound)
	require.Empty(t, drainQueue(queues.inventories))
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups))
	drainQueue(queues.projections)
	drainQueue(queues.status)

	revised := rebound.DeepCopy()
	revised.ResourceVersion = "3"
	revised.Spec.ProfileRef.Generation++
	router.rackUpdate(rebound, revised)
	require.Equal(t, []string{rack.Spec.InventoryRef.Name}, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))
}

func TestRackUpdateIgnoresStatusAndFinalizerOnlyChanges(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	var observed *mokkav1alpha1.SGPURack
	router.observeRackStatus = func(rack *mokkav1alpha1.SGPURack) { observed = rack }

	rack := testRack(testNode())
	updated := rack.DeepCopy()
	updated.ResourceVersion = "2"
	updated.Finalizers = []string{controllerack.RackFinalizer}
	updated.Status.AssignedNodes = 1
	router.rackUpdate(rack, updated)

	require.Same(t, updated, observed)
	require.Empty(t, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))
	require.Empty(t, drainQueue(queues.projections))
	require.Empty(t, drainQueue(queues.status))
}

func TestStaleRackDeleteDoesNotRouteClaimantPastSameNameReplacement(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	inventory := testInventory()
	require.NoError(t, inventories.Add(inventory))
	router.inventoryAdd(inventory)
	drainQueue(queues.inventories)
	drainQueue(queues.status)

	name := materialize.RackName(inventory.Name, inventory.UID, "group", 0)
	stale := &mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: name, UID: "stale-rack-uid"}}
	replacement := &mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: name, UID: "replacement-rack-uid"}}
	require.NoError(t, racks.Add(replacement))

	router.rackDelete(cache.DeletedFinalStateUnknown{Key: name, Obj: stale})

	require.Empty(t, drainQueue(queues.inventories))
	require.Empty(t, drainQueue(queues.groups))
}

func TestDesiredRackClaimantTracksInventoryReplacementAndShrink(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	oldInventory := testInventory()
	oldInventory.Spec.RackGroups[0].Count = 2
	require.NoError(t, inventories.Add(oldInventory))
	router.inventoryAdd(oldInventory)
	drainQueue(queues.inventories)
	drainQueue(queues.status)

	recreated := oldInventory.DeepCopy()
	recreated.UID = "recreated-inventory-uid"
	recreated.ResourceVersion = "2"
	recreated.Spec.RackGroups[0].Count = 1
	recreated.Spec.RackGroups[0].ProfileRef.Name = "replacement-profile"
	router.inventoryDelete(oldInventory)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	require.Zero(t, router.claims.size())
	require.NoError(t, inventories.Delete(oldInventory))
	require.NoError(t, inventories.Add(recreated))
	router.inventoryAdd(recreated)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	router.inventoryDelete(cache.DeletedFinalStateUnknown{Key: oldInventory.Name, Obj: oldInventory})
	drainQueue(queues.inventories)
	drainQueue(queues.status)

	oldName := materialize.RackName(oldInventory.Name, oldInventory.UID, "group", 0)
	removedName := materialize.RackName(oldInventory.Name, oldInventory.UID, "group", 1)
	currentName := materialize.RackName(recreated.Name, recreated.UID, "group", 0)
	for _, staleName := range []string{oldName, removedName} {
		router.rackDelete(&mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: staleName, UID: "stale-rack-uid"}})
		require.Empty(t, drainQueue(queues.inventories))
	}

	router.rackDelete(&mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: currentName, UID: "foreign-rack-uid"}})
	require.Equal(t, []string{recreated.Name}, drainQueue(queues.inventories))
	require.Equal(t, []statusKey{{kind: statusInventory, name: recreated.Name, uid: recreated.UID}}, drainQueue(queues.status))
}

func TestDesiredRackClaimIndexRetainsOnlyCurrentInformerTopology(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	current := testInventory()
	require.NoError(t, inventories.Add(current))
	router.inventoryAdd(current)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	require.Equal(t, 1, router.claims.size())

	for revision := 2; revision <= 2_000; revision++ {
		previous := current
		current = previous.DeepCopy()
		current.UID = types.UID(fmt.Sprintf("inventory-uid-%d", revision))
		current.ResourceVersion = strconv.Itoa(revision)
		current.Spec.RackGroups[0].Count = int32(revision%3 + 1)
		current.Spec.RackGroups[0].ProfileRef.Name = fmt.Sprintf("profile-%d", revision)
		require.NoError(t, inventories.Update(current))
		router.inventoryUpdate(previous, current)
		router.inventoryDelete(cache.DeletedFinalStateUnknown{Key: previous.Name, Obj: previous})
		drainQueue(queues.inventories)
		drainQueue(queues.status)
		require.Equal(t, int(current.Spec.RackGroups[0].Count), router.claims.size())
	}

	router.inventoryDelete(current)
	require.Zero(t, router.claims.size())
}

func TestDesiredRackClaimsTrackShrinkGroupRenameAndProfileRevision(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	router := newEventRouter(inventories, racks, newPlacementRegistry(), queues)
	current := testInventory()
	current.Spec.RackGroups[0].Count = 2
	require.NoError(t, inventories.Add(current))
	router.inventoryAdd(current)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	require.Equal(t, 2, router.claims.size())

	shrunk := current.DeepCopy()
	shrunk.ResourceVersion = "2"
	shrunk.Spec.RackGroups[0].Count = 1
	shrunk.Spec.RackGroups[0].ProfileRef.Name = "profile-v2"
	require.NoError(t, inventories.Update(shrunk))
	router.inventoryUpdate(current, shrunk)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	require.Equal(t, 1, router.claims.size())
	removed := materialize.RackName(current.Name, current.UID, "group", 1)
	router.rackDelete(&mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: removed, UID: "removed-rack-uid"}})
	require.Empty(t, drainQueue(queues.inventories))

	renamed := shrunk.DeepCopy()
	renamed.ResourceVersion = "3"
	renamed.Spec.RackGroups[0].ID = "renamed"
	require.NoError(t, inventories.Update(renamed))
	router.inventoryUpdate(shrunk, renamed)
	drainQueue(queues.inventories)
	drainQueue(queues.status)
	oldName := materialize.RackName(shrunk.Name, shrunk.UID, "group", 0)
	newName := materialize.RackName(renamed.Name, renamed.UID, "renamed", 0)
	router.rackDelete(&mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: oldName, UID: "old-rack-uid"}})
	require.Empty(t, drainQueue(queues.inventories))

	profile := &mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile-v2", UID: "profile-uid", Generation: 1}}
	revised := profile.DeepCopy()
	revised.Generation = 2
	revised.Spec.Rack.NodesPerRack = 2
	router.profileUpdate(profile, revised)
	require.Equal(t, []string{renamed.Name}, drainQueue(queues.inventories))
	drainQueue(queues.status)
	require.Equal(t, 1, router.claims.size())

	router.rackDelete(&mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: newName, UID: "foreign-rack-uid"}})
	require.Equal(t, []string{renamed.Name}, drainQueue(queues.inventories))
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

func TestProcessNextDistinguishesRequestTimeoutFromCallerShutdown(t *testing.T) {
	t.Run("request timeout retries while caller remains active", func(t *testing.T) {
		queue := workqueue.NewTypedRateLimitingQueue(
			workqueue.NewTypedItemFastSlowRateLimiter[string](0, 0, 1),
		)
		t.Cleanup(queue.ShutDown)
		queue.Add("key")

		require.True(t, processNext(context.Background(), queue, func(context.Context, string) error {
			return context.DeadlineExceeded
		}))
		require.Equal(t, 1, queue.NumRequeues("key"))
	})

	t.Run("caller cancellation never requeues a deadline", func(t *testing.T) {
		queue := workqueue.NewTypedRateLimitingQueue(
			workqueue.NewTypedItemFastSlowRateLimiter[string](0, 0, 1),
		)
		t.Cleanup(queue.ShutDown)
		queue.Add("key")
		ctx, cancel := context.WithCancel(context.Background())

		require.True(t, processNext(ctx, queue, func(context.Context, string) error {
			cancel()
			return context.DeadlineExceeded
		}))
		require.Zero(t, queue.NumRequeues("key"))
		require.Zero(t, queue.Len())
	})
}

func TestStatusIntervalOptionsPreserveLegacyConstructionAndRejectInvalidBounds(t *testing.T) {
	legacy := Options{Workers: 1, StatusDebounce: 100 * time.Millisecond}
	require.NoError(t, legacy.validate())
	require.Equal(t, time.Second, legacy.statusProgressInterval())

	invalid := legacy
	invalid.StatusProgressInterval = 50 * time.Millisecond
	require.ErrorContains(t, invalid.validate(), "shorter than status debounce")
}

func TestProcessNextStatusKeepsExistingRateLimitedRetry(t *testing.T) {
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.NewTypedItemFastSlowRateLimiter[statusKey](0, 0, 1),
	)
	statuses := newStatusCoalescer(queue, 0, time.Second, realStatusScheduler{})
	t.Cleanup(func() {
		statuses.shutdown()
		queue.ShutDown()
	})
	controller := &Controller{
		queues:          &queues{status: queue, statuses: statuses},
		reconcileStatus: func(context.Context, statusKey) error { return errors.New("retry") },
	}
	key := testInventoryStatusKey()
	statuses.dirty(key)

	require.True(t, controller.processNextStatus(context.Background()))
	require.Equal(t, 1, queue.NumRequeues(key))
	require.Equal(t, 1, queue.Len())

	controller.reconcileStatus = func(context.Context, statusKey) error { return nil }
	require.True(t, controller.processNextStatus(context.Background()))
	require.Zero(t, queue.NumRequeues(key))
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
		return context.Cause(ctx)
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
	require.Zero(t, controller.queues.inventories.NumRequeues("inventory"))
}

func TestFilteredNodeListWatchUsesServerSideSelector(t *testing.T) {
	nodes := &recordingNodeAPI{watcher: watch.NewFake()}
	listWatch := newFilteredNodeListWatch(nodes)
	_, err := listWatch.ListWithContext(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	watcher, err := listWatch.WatchWithContext(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	watcher.Stop()
	require.Equal(t, []string{
		allocate.EligibleNodeLabel + "=true",
		allocate.EligibleNodeLabel + "=true",
	}, nodes.selectors())
}

func TestCompactNodeObjectRetainsOnlyControllerReadSurface(t *testing.T) {
	deleting := metav1.NewTime(time.Unix(100, 0))
	node := testNode()
	node.CreationTimestamp = metav1.NewTime(time.Unix(50, 0))
	node.DeletionTimestamp = &deleting
	node.Annotations = map[string]string{
		controllerprojection.AssignmentAnnotation: "assignment",
		"foreign": "large-unrelated-value",
	}
	setNodeManagedFields(node, controllerprojection.FieldManager,
		[]string{controllerprojection.CliqueLabel}, []string{controllerprojection.AssignmentAnnotation})
	setNodeManagedFields(node, "foreign-controller", []string{controllerprojection.AssignedLabel}, nil)
	node.ManagedFields[1].FieldsV1 = metav1.NewFieldsV1(
		`{"f:metadata":{"f:labels":{"f:` + controllerprojection.AssignedLabel + `":{}}},"f:spec":{"f:podCIDR":{}}}`,
	)
	node.ManagedFields = append(node.ManagedFields, metav1.ManagedFieldsEntry{
		Manager: "unrelated-controller", Operation: metav1.ManagedFieldsOperationUpdate,
		APIVersion: "v1", FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(`{"f:spec":{"f:podCIDRs":{}}}`),
	})
	node.Spec.PodCIDR = "10.0.0.0/24"
	node.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.1"}}

	object, err := compactNodeObject(node)
	require.NoError(t, err)
	compact, ok := object.(*corev1.Node)
	require.True(t, ok)
	require.Equal(t, node.Name, compact.Name)
	require.Equal(t, node.UID, compact.UID)
	require.Equal(t, node.ResourceVersion, compact.ResourceVersion)
	require.Equal(t, node.CreationTimestamp, compact.CreationTimestamp)
	require.Equal(t, node.DeletionTimestamp, compact.DeletionTimestamp)
	require.Equal(t, node.Labels, compact.Labels)
	require.Equal(t, "assignment", compact.Annotations[controllerprojection.AssignmentAnnotation])
	require.Len(t, compact.Annotations, 1)
	require.Len(t, compact.ManagedFields, 2)
	require.Equal(t, controllerprojection.FieldManager, compact.ManagedFields[0].Manager)
	require.Equal(t, "foreign-controller", compact.ManagedFields[1].Manager)
	require.NotContains(t, compact.ManagedFields[1].FieldsV1.GetRawString(), "podCIDR")
	require.Empty(t, compact.Spec)
	require.Empty(t, compact.Status)
}

func TestNodeSpecUpdateDoesNotRouteAllocationWork(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	registry.replace(testInventory())
	router := newEventRouter(inventories, racks, registry, queues)

	oldNode := testNode()
	current := oldNode.DeepCopy()
	current.ResourceVersion = "2"
	current.Spec.Unschedulable = true
	current.Spec.Taints = []corev1.Taint{{Key: "maintenance", Effect: corev1.TaintEffectNoSchedule}}
	router.nodeUpdate(oldNode, current)

	require.Empty(t, drainQueue(queues.groups))
	require.Empty(t, drainQueue(queues.projections))
	require.Empty(t, drainQueue(queues.status))
}

func TestSingleNodeEventDoesNotListFromAPI(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
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
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
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
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	inventory := testInventory()
	registry.replace(inventory)
	node := testNode()
	rack := testRack(node)
	rack.Spec.Identity.FabricUUID = "fabric"
	require.NoError(t, racks.Add(rack))
	router := newEventRouter(inventories, racks, registry, queues)

	projected := node.DeepCopy()
	projected.ResourceVersion = "2"
	projected.Labels[metadata.AssignedLabel] = "true"
	projected.Labels[metadata.CliqueLabel] = "fabric.0"
	assignment, err := controllerprojection.EncodeAssignment(rack, &rack.Spec.Nodes[0])
	require.NoError(t, err)
	projected.Annotations = map[string]string{metadata.AssignmentAnnotation: assignment}
	setNodeManagedFields(projected, controllerprojection.FieldManager,
		[]string{metadata.AssignedLabel, metadata.CliqueLabel}, []string{metadata.AssignmentAnnotation})
	router.nodeUpdate(node, projected)
	require.Empty(t, drainQueue(queues.groups))
	require.Empty(t, drainQueue(queues.projections), "the successful projection event must not enqueue itself")
	require.ElementsMatch(t, []statusKey{
		{kind: statusInventory, name: "inventory", uid: "inventory-uid"},
		{kind: statusRack, name: "rack", uid: "rack-uid"},
	}, drainQueue(queues.status), "cached projection metadata must publish its durable status input")

	damaged := projected.DeepCopy()
	damaged.ResourceVersion = "3"
	delete(damaged.Labels, metadata.AssignedLabel)
	router.nodeUpdate(projected, damaged)
	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups))
	require.Equal(t, []projectionKey{{mode: projectionApply, rackName: rack.Name, nodeIndex: 0}}, drainQueue(queues.projections),
		"external removal of "+metadata.AssignedLabel+" must enqueue repair")
}

func TestForeignProjectionCoOwnerEventRoutesExactBinding(t *testing.T) {
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	registry.replace(testInventory())
	node := testNode()
	rack := testRack(node)
	require.NoError(t, racks.Add(rack))
	router := newEventRouter(inventories, racks, registry, queues)

	projected := node.DeepCopy()
	projected.ResourceVersion = "2"
	projected.Labels[metadata.AssignedLabel] = "true"
	assignment, err := controllerprojection.EncodeAssignment(rack, &rack.Spec.Nodes[0])
	require.NoError(t, err)
	projected.Annotations = map[string]string{metadata.AssignmentAnnotation: assignment}
	setNodeManagedFields(projected, controllerprojection.FieldManager,
		[]string{metadata.AssignedLabel}, []string{metadata.AssignmentAnnotation})
	coOwned := projected.DeepCopy()
	coOwned.ResourceVersion = "3"
	setNodeManagedFields(coOwned, "foreign-controller", nil, []string{metadata.AssignmentAnnotation})

	router.nodeUpdate(projected, coOwned)

	require.Equal(t, []allocate.GroupKey{testGroupKey()}, drainQueue(queues.groups))
	require.Equal(t, []projectionKey{{mode: projectionApply, rackName: rack.Name, nodeIndex: 0}}, drainQueue(queues.projections))
	require.ElementsMatch(t, []statusKey{
		{kind: statusInventory, name: "inventory", uid: "inventory-uid"},
		{kind: statusRack, name: "rack", uid: "rack-uid"},
	}, drainQueue(queues.status))
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
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
			ID: "group", Count: 1,
			ProfileRef: mokkav1alpha1.ProfileReference{Name: "profile"},
			Placement:  &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "a"}}},
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
			Nodes: []mokkav1alpha1.SGPURackNode{{
				Index: 0, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID},
			}},
		},
	}
}

func testGroupKey() allocate.GroupKey {
	return allocate.GroupKey{InventoryName: "inventory", InventoryUID: "inventory-uid", RackGroup: "group"}
}

func setNodeManagedFields(node *corev1.Node, manager string, labelKeys, annotationKeys []string) {
	metadataFields := make(map[string]any)
	if len(labelKeys) > 0 {
		fields := make(map[string]any, len(labelKeys))
		for _, key := range labelKeys {
			fields["f:"+key] = map[string]any{}
		}
		metadataFields["f:labels"] = fields
	}
	if len(annotationKeys) > 0 {
		fields := make(map[string]any, len(annotationKeys))
		for _, key := range annotationKeys {
			fields["f:"+key] = map[string]any{}
		}
		metadataFields["f:annotations"] = fields
	}
	raw, err := json.Marshal(map[string]any{"f:metadata": metadataFields})
	if err != nil {
		panic(err)
	}
	node.ManagedFields = append(node.ManagedFields, metav1.ManagedFieldsEntry{
		Manager: manager, Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1",
		FieldsType: "FieldsV1", FieldsV1: metav1.NewFieldsV1(string(raw)),
	})
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
