// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	sgpuinventory "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/allocate"
	nodecatalog "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/nodecatalog"
	inventoryprojection "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/projection"
	rackrender "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/rack"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
)

func TestRackBindingReleaseWakesDestinationAfterEarlierWorkDrains(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.InventoryIndexers())
	profiles := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.Indexers())
	catalog := nodecatalog.New()
	queues := newQueues(0)
	t.Cleanup(queues.shutdown)
	registry := newPlacementRegistry()
	profile := acceptanceProfile(1)
	source := testInventory()
	source.Finalizers = []string{sgpuinventory.InventoryFinalizer}
	source.Spec.RackGroups[0].ProfileRef.Name = profile.Name
	destination := source.DeepCopy()
	destination.Name, destination.UID = "destination", "destination-uid"
	destination.Spec.RackGroups[0].Placement.NodeSelector.MatchLabels["pool"] = "b"
	node := testNode()
	liveNodes := newAcceptanceNodeClient()
	liveNodes.create(node)
	catalog.Upsert(node)
	mokka := mokkafake.NewSimpleClientset(profile, source, destination)
	installAcceptanceAPIReactors(t, mokka)
	require.NoError(t, profiles.Add(profile))
	for _, inventory := range []*mokkav1alpha1.SGPUInventory{source, destination} {
		require.NoError(t, inventories.Add(inventory))
		registry.replace(inventory)
		rendered, err := rackrender.RenderRack(rackrender.RackInput{
			InventoryName: inventory.Name, InventoryUID: inventory.UID,
			Group: inventory.Spec.RackGroups[0], Profile: profile,
		})
		require.NoError(t, err)
		rack := &mokkav1alpha1.SGPURack{
			ObjectMeta: metav1.ObjectMeta{
				Name: rendered.Name, Finalizers: []string{sgpuinventory.RackFinalizer},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
					inventory, mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"),
				)},
			},
			Spec: rendered.Spec,
		}
		if inventory == source {
			rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}
		}
		stored, err := mokka.MokkaV1alpha1().SGPURacks().Create(ctx, rack, metav1.CreateOptions{
			FieldManager: sgpuinventory.RackFieldManager,
		})
		require.NoError(t, err)
		require.NoError(t, racks.Add(stored))
	}
	snapshot := newInformerCache(
		mokkalisters.NewSGPUInventoryLister(inventories), mokkalisters.NewSGPURackProfileLister(profiles),
		racks, catalog, liveNodes, DefaultOptions(),
	)
	allocation := sgpuinventory.NewAllocationCache(snapshot)
	projection := inventoryprojection.NewController(snapshot, liveNodes)
	reconciler := sgpuinventory.NewReconcilerWithAllocationCache(
		snapshot, mokka.MokkaV1alpha1().SGPUInventories(), mokka.MokkaV1alpha1().SGPURacks(), projection, allocation,
	)
	router := newEventRouter(inventories, racks, catalog, registry, queues,
		allocation.InvalidateAllocation, allocation.InvalidateCapacity)
	sourceKey, destinationKey := groupKey(source, "group"), groupKey(destination, "group")
	sourceRackName := rackrender.RackName(source.Name, source.UID, "group", 0)
	destinationRackName := rackrender.RackName(destination.Name, destination.UID, "group", 0)
	_, err := projection.ProjectFresh(ctx, sourceRackName, 0)
	require.NoError(t, err)
	projected := liveNodes.snapshot(node.Name)
	require.True(t, nodeIsProjected(projected, node.UID))
	catalog.Upsert(projected)

	liveNodes.update(node.Name, func(current *corev1.Node) { current.Labels["pool"] = "b" })
	moved := liveNodes.snapshot(node.Name)
	catalog.Upsert(moved)
	router.nodeUpdate(projected, moved)
	require.ElementsMatch(t, []allocate.RackGroupKey{sourceKey, destinationKey}, drainQueue(queues.groups))
	pending, err := reconciler.ReconcileGroup(ctx, destinationKey)
	require.NoError(t, err)
	require.Len(t, pending.Allocation.Pending, 1, "destination must wait for the source's durable binding")
	require.Empty(t, pending.Allocation.Assigned)
	releasing, err := reconciler.ReconcileGroup(ctx, sourceKey)
	require.NoError(t, err)
	require.Len(t, releasing.CleanupNeeded, 1)
	_, err = projection.Cleanup(ctx, releasing.CleanupNeeded[0])
	require.NoError(t, err)
	require.True(t, projection.Ready(releasing.CleanupNeeded[0]))

	cleaned := liveNodes.snapshot(node.Name)
	require.False(t, nodeHasProjection(cleaned))
	catalog.Upsert(cleaned)
	router.nodeUpdate(moved, cleaned)
	require.Equal(t, []allocate.RackGroupKey{destinationKey}, drainQueue(queues.groups))
	pending, err = reconciler.ReconcileGroup(ctx, destinationKey)
	require.NoError(t, err)
	require.Len(t, pending.Allocation.Pending, 1, "the cleanup event still precedes durable release")
	require.Empty(t, pending.Allocation.Assigned)
	drainQueue(queues.projections)
	drainQueue(queues.status)
	require.Zero(t, queues.inventories.Len())

	oldRack, err := snapshot.Rack(sourceRackName)
	require.NoError(t, err)
	_, err = reconciler.ReconcileGroup(ctx, sourceKey)
	require.NoError(t, err)
	releasedRack, err := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, sourceRackName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Nil(t, releasedRack.Spec.Nodes[0].NodeRef)
	require.NoError(t, racks.Update(releasedRack))
	router.rackUpdate(oldRack, releasedRack)
	require.ElementsMatch(t, []allocate.RackGroupKey{sourceKey, destinationKey}, drainQueue(queues.groups),
		"durable release must wake the destination even after both earlier Node events were processed")
	require.Contains(t, drainQueue(queues.status), statusKey{
		kind: statusInventory, name: destination.Name, uid: destination.UID,
	})

	assigned, err := reconciler.ReconcileGroup(ctx, destinationKey)
	require.NoError(t, err)
	require.Empty(t, assigned.Allocation.Pending)
	require.Len(t, assigned.Allocation.Assigned, 1)
	bound, err := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, destinationRackName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}, bound.Spec.Nodes[0].NodeRef)
}

func TestRackBindingReleaseRoutesOnlyCurrentExactNodePlacement(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"update", "delete"} {
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			for _, scenario := range []string{"exact", "missing", "replacement UID", "different name", "ineligible", "nonmatching"} {
				t.Run(scenario, func(t *testing.T) {
					t.Parallel()
					inventories := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.InventoryIndexers())
					racks := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.Indexers())
					catalog := nodecatalog.New()
					queues := newQueues(0)
					t.Cleanup(queues.shutdown)
					registry := newPlacementRegistry()
					destination := testInventory()
					destination.Name, destination.UID = "destination", "destination-uid"
					registry.replace(destination)
					node := testNode()
					rack := testRack(node)
					switch scenario {
					case "replacement UID":
						node.UID = "replacement-uid"
					case "different name":
						node.Name = "other-node"
					case "ineligible":
						delete(node.Labels, allocate.EligibleNodeLabel)
					case "nonmatching":
						node.Labels["pool"] = "other"
					}
					if scenario != "missing" {
						catalog.Upsert(node)
					}
					router := newEventRouter(inventories, racks, catalog, registry, queues)
					if event == "update" {
						released := rack.DeepCopy()
						released.Spec.Nodes[0].NodeRef = nil
						require.NoError(t, racks.Add(released))
						router.rackUpdate(rack, released)
					} else {
						router.rackDelete(cache.DeletedFinalStateUnknown{Key: rack.Name, Obj: rack})
					}
					groups := drainQueue(queues.groups)
					statuses := drainQueue(queues.status)
					destinationKey := groupKey(destination, "group")
					destinationStatus := statusKey{kind: statusInventory, name: destination.Name, uid: destination.UID}
					if scenario == "exact" {
						require.Contains(t, groups, destinationKey)
						require.Contains(t, statuses, destinationStatus)
					} else {
						require.NotContains(t, groups, destinationKey)
						require.NotContains(t, statuses, destinationStatus)
					}
					require.Len(t, drainQueue(queues.projections), 1, "release must still enqueue exact projection cleanup")
				})
			}
		})
	}
}
