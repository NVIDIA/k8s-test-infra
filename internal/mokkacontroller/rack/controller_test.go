// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package rack

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

func TestReconcileCreatesDeterministicRacksAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 2, 2)
	inventory := testInventory("inventory", "inventory-uid", "p", 2)
	nodes := []*corev1.Node{
		testNode("later", "later-uid", 2, map[string]string{"pool": "gpu"}),
		testNode("first", "first-uid", 1, map[string]string{"pool": "gpu"}),
		testNode("third", "third-uid", 3, map[string]string{"pool": "gpu"}),
	}
	h := newHarness(t, []runtime.Object{profile, inventory}, nodes)

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	require.True(t, result.ResolvedRefs)
	require.Empty(t, result.CleanupNeeded)
	require.Empty(t, result.OwnershipConflicts)

	storedInventory, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, storedInventory.Finalizers, InventoryFinalizer)

	racks, err := h.mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, racks.Items, 2)
	for rackIndex := int32(0); rackIndex < 2; rackIndex++ {
		name := materialize.RackName(inventory.Name, inventory.UID, "group", rackIndex)
		got, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, rackIndex, got.Spec.Identity.RackIndex)
		require.Equal(t, []string{RackFinalizer}, got.Finalizers)
		require.Equal(t, string(inventory.UID), got.Annotations[InventoryUIDAnnotation])
		require.Equal(t, inventory.Name, got.Labels[InventoryNameLabel])
		require.Equal(t, "group", got.Labels[RackGroupLabel])
		require.True(t, metav1.IsControlledBy(got, inventory))
	}

	firstRack, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, types.UID("first-uid"), firstRack.Spec.Slots[0].NodeRef.UID)
	require.Equal(t, types.UID("later-uid"), firstRack.Spec.Slots[1].NodeRef.UID)

	h.sync(t)
	h.mokka.Fake.ClearActions()
	result, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.Changed)
	require.Empty(t, h.mokka.Actions())

	newNode := testNode("sorts-before", "new-uid", 0, map[string]string{"pool": "gpu"})
	h.nodes = append(h.nodes, newNode)
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	unchurned, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, firstRack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, types.UID("first-uid"), unchurned.Spec.Slots[0].NodeRef.UID)
	require.Equal(t, types.UID("later-uid"), unchurned.Spec.Slots[1].NodeRef.UID)
}

func TestReconcileReportsOverlapAndRetainsLastGoodRackForMissingProfile(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	other := testInventory("other", "other-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory, other}, []*corev1.Node{node})

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.Len(t, result.Allocation.Conflicts, 1)
	require.Equal(t, allocate.ConflictSelectorOverlap, result.Allocation.Conflicts[0].Kind)
	require.Empty(t, result.Allocation.Assigned)

	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuprofiles"), "", profile.Name))
	h.sync(t)
	h.mokka.Fake.ClearActions()
	result, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.ResolvedRefs)
	require.Len(t, result.ProfileIssues, 1)
	require.Empty(t, h.mokka.Actions(), "missing profiles retain the last-good rack set")
}

func TestReconcileExistingBindingWinsNewSelectorOverlap(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	overlapping := testInventory("other", "other-uid", "p", 1)
	require.NoError(t, h.mokka.Tracker().Add(overlapping))
	h.sync(t)
	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.Empty(t, result.Allocation.Conflicts)

	got, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, node.UID, got.Spec.Slots[0].NodeRef.UID)
}

func TestReconcileInvalidInventoryAndProfileRetainLastGoodRacks(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	invalidProfile, err := h.mokka.MokkaV1alpha1().SGPUProfiles().Get(ctx, profile.Name, metav1.GetOptions{})
	require.NoError(t, err)
	invalidProfile.Spec.Node.Topology.GPUSlots[0].PCIAddress = "not-a-pci-address"
	_, err = h.mokka.MokkaV1alpha1().SGPUProfiles().Update(ctx, invalidProfile, metav1.UpdateOptions{})
	require.NoError(t, err)
	h.sync(t)
	h.mokka.Fake.ClearActions()
	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	require.False(t, result.ResolvedRefs)
	require.Len(t, result.ProfileIssues, 1)
	require.Empty(t, h.mokka.Actions())

	validProfile := testProfile("p", "profile-recreated", 1, 1, 1)
	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuprofiles"), "", profile.Name))
	require.NoError(t, h.mokka.Tracker().Add(validProfile))
	invalidInventory, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	invalidInventory.Spec.RackGroups[0].Count = -1
	_, err = h.mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, invalidInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	h.sync(t)
	h.mokka.Fake.ClearActions()
	result, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.NotEmpty(t, result.ValidationError)
	require.Empty(t, h.mokka.Actions())
}

func TestReconcileRendersRecreatedProfileWithoutMovingBindings(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "old-profile-uid", 1, 2, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	recreated := testProfile("p", "new-profile-uid", 1, 2, 2)
	recreated.Spec.Node.Topology.GPUSlots[1].PCIAddress = "0000:03:00.0"
	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuprofiles"), "", profile.Name))
	require.NoError(t, h.mokka.Tracker().Add(recreated))
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	got, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, recreated.UID, got.Spec.ProfileRef.UID)
	require.Equal(t, "0000:03:00.0", got.Spec.Slots[0].GPUs[1].PCIAddress)
	require.Equal(t, node.UID, got.Spec.Slots[0].NodeRef.UID)
}

func TestReconcileShrinkWaitsForCleanupThenRetires(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 2, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 2)
	nodes := []*corev1.Node{
		testNode("one", "one-uid", 1, map[string]string{"pool": "gpu"}),
		testNode("two", "two-uid", 2, map[string]string{"pool": "gpu"}),
		testNode("three", "three-uid", 3, map[string]string{"pool": "gpu"}),
	}
	h := newHarness(t, []runtime.Object{profile, inventory}, nodes)
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	updatedProfile, err := h.mokka.MokkaV1alpha1().SGPUProfiles().Get(ctx, profile.Name, metav1.GetOptions{})
	require.NoError(t, err)
	updatedProfile.Spec.Rack.NodesPerRack = 1
	updatedProfile.Spec.Node.Topology.GPUFabric.Domain.GPUCount = 1
	updatedProfile.Generation++
	_, err = h.mokka.MokkaV1alpha1().SGPUProfiles().Update(ctx, updatedProfile, metav1.UpdateOptions{})
	require.NoError(t, err)
	updatedInventory, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	updatedInventory.Spec.RackGroups[0].Count = 1
	updatedInventory.Generation++
	_, err = h.mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, updatedInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	h.sync(t)

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.Len(t, result.CleanupNeeded, 2)
	surviving, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Len(t, surviving.Spec.Slots, 2, "a bound high slot remains until projection cleanup")
	retiringName := materialize.RackName(inventory.Name, inventory.UID, "group", 1)
	_, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, retiringName, metav1.GetOptions{})
	require.NoError(t, err)

	h.cleaned = true
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	shrunk, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, surviving.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, shrunk.Spec.Slots, 1)
	_, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, retiringName, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestReconcileHandlesOwnerConflictAndRetriesOptimisticConflict(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	name := materialize.RackName(inventory.Name, inventory.UID, "group", 0)
	foreign := &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: "foreign-rack-uid"},
		Spec: mokkav1alpha1.SGPURackSpec{InventoryRef: mokkav1alpha1.SGPURackInventoryReference{
			Name: "foreign", UID: "foreign-inventory-uid",
		}},
	}
	h := newHarness(t, []runtime.Object{profile, inventory, foreign}, nil)

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.Len(t, result.OwnershipConflicts, 1)
	require.Equal(t, name, result.OwnershipConflicts[0].RackName)
	for _, action := range h.mokka.Actions() {
		if action.GetResource().Resource == "sgpuracks" {
			require.False(t, action.Matches("create", "sgpuracks"), "cached foreign ownership is detected before mutation")
		}
	}
	got, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, foreign.Spec, got.Spec)

	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks"), "", name))
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)
	profile.Generation++
	profile.Spec.Software.DriverVersion = "new"
	require.NoError(t, h.mokka.Tracker().Update(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuprofiles"), profile, ""))
	h.sync(t)

	conflicted := false
	h.mokka.PrependReactor("update", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if conflicted {
			return false, nil, nil
		}
		conflicted = true
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: mokkav1alpha1.GroupName, Resource: "sgpuracks"}, name, nil)
	})
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.True(t, conflicted)
}

func TestReconcileWaitsForGoneUIDCleanupBeforeAllocatingReplacement(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	old := testNode("same", "old-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{old})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	replacement := testNode("same", "new-uid", 2, map[string]string{"pool": "gpu"})
	h.nodes = []*corev1.Node{replacement}
	h.sync(t)
	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.Len(t, result.CleanupNeeded, 1)
	got, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, old.UID, got.Spec.Slots[0].NodeRef.UID)

	h.cleaned = true
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	got, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, replacement.UID, got.Spec.Slots[0].NodeRef.UID)
}

func TestReconcileDeletionFinalizersAndManualRackDeletion(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "group", 0)
	deletingRack, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rackName, metav1.GetOptions{})
	require.NoError(t, err)
	now := metav1.Now()
	deletingRack.DeletionTimestamp = &now
	require.NoError(t, h.mokka.Tracker().Update(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks"), deletingRack, ""))
	h.sync(t)
	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.Len(t, result.CleanupNeeded, 1)

	h.cleaned = true
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	_, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rackName, metav1.GetOptions{})
	require.NoError(t, err, "a manually deleted desired rack is recreated")

	deletingInventory, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	deletingInventory.DeletionTimestamp = &now
	require.NoError(t, h.mokka.Tracker().Update(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuinventories"), deletingInventory, ""))
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	finished, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotContains(t, finished.Finalizers, InventoryFinalizer)
}

func TestInformerIndexesExposeOnlyDirectDependents(t *testing.T) {
	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, InventoryIndexers())
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	inventory.Spec.RackGroups = append(inventory.Spec.RackGroups, mokkav1alpha1.SGPURackGroup{
		ID: "second", ProfileRef: corev1.LocalObjectReference{Name: "p"},
	})
	require.NoError(t, inventoryIndexer.Add(inventory))
	byProfile, err := inventoryIndexer.ByIndex(InventoryByProfileNameIndex, "p")
	require.NoError(t, err)
	require.Equal(t, []any{inventory}, byProfile)

	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, RackIndexers())
	rack := &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rack",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
				inventory,
				mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"),
			)},
		},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
			Slots: []mokkav1alpha1.SGPURackSlot{
				{Index: 0, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}},
				{Index: 1, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}},
			},
		},
	}
	require.NoError(t, rackIndexer.Add(rack))
	for indexName, key := range map[string]string{
		RackByInventoryUIDIndex:   string(inventory.UID),
		RackByInventoryGroupIndex: InventoryGroupIndexKey(inventory.UID, "group"),
		RackByNodeUIDIndex:        "node-uid",
		RackByNodeNameIndex:       "node",
	} {
		indexed, err := rackIndexer.ByIndex(indexName, key)
		require.NoError(t, err)
		require.Equal(t, []any{rack}, indexed)
	}
}

type harness struct {
	t       *testing.T
	mokka   *mokkafake.Clientset
	nodes   []*corev1.Node
	cache   *ListerCache
	cleaned bool
}

func newHarness(t *testing.T, mokkaObjects []runtime.Object, nodes []*corev1.Node) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		mokka: mokkafake.NewSimpleClientset(mokkaObjects...),
		nodes: nodes,
	}
	h.sync(t)
	return h
}

func (h *harness) sync(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, InventoryIndexers())
	profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, RackIndexers())
	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	inventories, err := h.mokka.MokkaV1alpha1().SGPUInventories().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	for i := range inventories.Items {
		require.NoError(t, inventoryIndexer.Add(inventories.Items[i].DeepCopy()))
	}
	profiles, err := h.mokka.MokkaV1alpha1().SGPUProfiles().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	for i := range profiles.Items {
		require.NoError(t, profileIndexer.Add(profiles.Items[i].DeepCopy()))
	}
	racks, err := h.mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	for i := range racks.Items {
		require.NoError(t, rackIndexer.Add(racks.Items[i].DeepCopy()))
	}
	for i := range h.nodes {
		require.NoError(t, nodeIndexer.Add(h.nodes[i].DeepCopy()))
	}

	h.cache = NewListerCache(
		mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
		mokkalisters.NewSGPUProfileLister(profileIndexer),
		rackIndexer,
		NewNodeLister(nodeIndexer),
	)
}

func (h *harness) reconcile(ctx context.Context, key string) (Result, error) {
	reconciler := NewReconciler(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return h.cleaned }),
	)
	return reconciler.Reconcile(ctx, key)
}

func testInventory(name string, uid types.UID, profileName string, count int32) *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		TypeMeta:   metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid, Generation: 1},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.SGPURackGroup{{
			ID: "group", Count: count,
			ProfileRef: corev1.LocalObjectReference{Name: profileName},
			Placement: &mokkav1alpha1.SGPUPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "gpu"},
			}},
		}}},
	}
}

func testProfile(name string, uid types.UID, generation int64, nodesPerRack, gpuCount int32) *mokkav1alpha1.SGPUProfile {
	gpuSlots := make([]mokkav1alpha1.SGPUGPUSlot, gpuCount)
	for i := range gpuSlots {
		gpuSlots[i] = mokkav1alpha1.SGPUGPUSlot{
			Index: int32(i), PCIAddress: "0000:0" + string(rune('1'+i)) + ":00.0",
			RootComplex: "pci0000:00", NUMANode: int32(i), HostProcessorIndex: int32(i),
		}
	}
	return &mokkav1alpha1.SGPUProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid, Generation: generation},
		Spec: mokkav1alpha1.SGPUProfileSpec{
			Rack: mokkav1alpha1.SGPUProfileRack{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUProfileNode{
				GPUs: mokkav1alpha1.SGPUHardware{Count: gpuCount},
				Topology: mokkav1alpha1.SGPUNodeTopology{
					GPUSlots: gpuSlots,
					GPUFabric: &mokkav1alpha1.SGPUGPUFabric{Domain: mokkav1alpha1.SGPUGPUFabricDomain{
						GPUCount: nodesPerRack * gpuCount,
					}},
				},
			},
			Software: mokkav1alpha1.SGPUSoftware{DriverVersion: "driver"},
		},
	}
}

func testNode(name string, uid types.UID, creationSeconds int64, extraLabels map[string]string) *corev1.Node {
	labels := map[string]string{allocate.EligibleNodeLabel: "true"}
	for key, value := range extraLabels {
		labels[key] = value
	}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: name, UID: uid, Labels: labels,
		CreationTimestamp: metav1.NewTime(time.Unix(creationSeconds, 0)),
	}}
}
