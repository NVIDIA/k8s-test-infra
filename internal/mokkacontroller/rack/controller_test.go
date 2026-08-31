// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
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

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/materialize"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/metadata"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
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
	require.Equal(t, types.UID("first-uid"), firstRack.Spec.Nodes[0].NodeRef.UID)
	require.Equal(t, types.UID("later-uid"), firstRack.Spec.Nodes[1].NodeRef.UID)

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
	require.Equal(t, types.UID("first-uid"), unchurned.Spec.Nodes[0].NodeRef.UID)
	require.Equal(t, types.UID("later-uid"), unchurned.Spec.Nodes[1].NodeRef.UID)
}

func TestReconcileCreatesCacheMissingRacksWithOneWriteEach(t *testing.T) {
	const rackCount = 8
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, rackCount)
	inventory.Finalizers = []string{InventoryFinalizer}
	h := newHarness(t, []runtime.Object{profile, inventory}, nil)
	h.mokka.Fake.ClearActions()

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.True(t, result.Changed)

	actionCounts := map[string]int{}
	for _, action := range h.mokka.Actions() {
		if action.GetResource().Resource != "sgpuracks" {
			continue
		}
		actionCounts[action.GetVerb()]++
		if action.GetVerb() == "create" {
			create := action.(k8stesting.CreateActionImpl)
			require.Equal(t, RackFieldManager, create.GetCreateOptions().FieldManager)
			rack := create.GetObject().(*mokkav1alpha1.SGPURack)
			require.True(t, metav1.IsControlledBy(rack, inventory))
			owner := metav1.GetControllerOf(rack)
			require.NotNil(t, owner)
			require.NotNil(t, owner.BlockOwnerDeletion)
			require.False(t, *owner.BlockOwnerDeletion)
			require.Contains(t, rack.Finalizers, RackFinalizer)
			require.Empty(t, rack.Status.Conditions)
		}
	}
	require.Equal(t, map[string]int{"create": rackCount}, actionCounts)
}

func TestReconcileComputesRevisionOncePerProfileObservation(t *testing.T) {
	ctx := context.Background()
	shared := testProfile("shared", "shared-profile-uid", 7, 1, 1)
	other := testProfile("other", "other-profile-uid", 3, 1, 1)
	other.Spec.Software.DriverVersion = "other-driver"
	inventory := testInventory("inventory", "inventory-uid", shared.Name, 2)
	inventory.Finalizers = []string{InventoryFinalizer}
	second := inventory.Spec.RackGroups[0]
	second.ID = "second"
	second.Count = 3
	third := inventory.Spec.RackGroups[0]
	third.ID = "third"
	third.Count = 1
	third.ProfileRef.Name = other.Name
	inventory.Spec.RackGroups = append(inventory.Spec.RackGroups, second, third)
	h := newHarness(t, []runtime.Object{shared, other, inventory}, nil)

	type observation struct {
		uid        types.UID
		generation int64
	}
	calls := make(map[observation]int)
	reconciler := NewReconciler(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return false }),
	)
	reconciler.precomputeProfileRevision = func(
		profile *mokkav1alpha1.SGPURackProfile,
	) (materialize.PrecomputedProfileRevision, error) {
		calls[observation{uid: profile.UID, generation: profile.Generation}]++
		return materialize.PrecomputeProfileRevision(profile)
	}

	result, err := reconciler.Reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.True(t, result.ResolvedRefs)
	require.EqualValues(t, 6, result.Work.RacksReconciled)
	require.Equal(t, map[observation]int{
		{uid: shared.UID, generation: shared.Generation}: 1,
		{uid: other.UID, generation: other.Generation}:   1,
	}, calls)

	sharedRevision, err := materialize.ProfileRevision(shared.Spec)
	require.NoError(t, err)
	otherRevision, err := materialize.ProfileRevision(other.Spec)
	require.NoError(t, err)
	racks, err := h.mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, racks.Items, 6)
	for _, rack := range racks.Items {
		want := sharedRevision
		if rack.Spec.Identity.RackGroup == third.ID {
			want = otherRevision
		}
		require.Equal(t, want, rack.Spec.ProfileRef.Revision)
	}
}

func TestSupportedInventoryCapacityBoundariesAndOverflow(t *testing.T) {
	profile := testProfile("p", "profile-uid", 1, 1, 64)
	group := mokkav1alpha1.RackGroup{ID: "group", Count: 100_000}

	boundary, err := CapacityForGroup(group, profile)
	require.NoError(t, err)
	require.NoError(t, ValidateSupportedCapacity(boundary))
	require.Equal(t, DeclaredCapacity{Racks: 100_000, Nodes: 100_000, GPUs: 6_400_000}, boundary)
	require.EqualError(t,
		ValidateSupportedCapacity(DeclaredCapacity{Racks: 100_001, Nodes: 100_000}),
		"desired racks 100001 exceed supported maximum 100000",
	)
	require.EqualError(t,
		ValidateSupportedCapacity(DeclaredCapacity{Racks: 100_000, Nodes: 100_000, GPUs: math.MaxInt32 + 1}),
		"declared capacity exceeds int32 status bounds",
	)

	over, err := CapacityForGroup(mokkav1alpha1.RackGroup{ID: "group", Count: 100_001}, profile)
	require.NoError(t, err)
	require.EqualError(t, ValidateSupportedCapacity(over), "desired Nodes 100001 exceed supported maximum 100000")

	extreme := profile.DeepCopy()
	extreme.Spec.Rack.NodesPerRack = math.MaxInt32
	extreme.Spec.Node.GPUs.Count = math.MaxInt32
	_, err = CapacityForGroup(mokkav1alpha1.RackGroup{ID: "group", Count: math.MaxInt32}, extreme)
	require.EqualError(t, err, `rack group "group" GPU capacity overflows int64`)

	_, err = AddCapacity(
		DeclaredCapacity{Racks: math.MaxInt64, Nodes: math.MaxInt64, GPUs: math.MaxInt64},
		DeclaredCapacity{Racks: 1, Nodes: 1, GPUs: 1},
	)
	require.EqualError(t, err, "aggregate rack capacity overflows int64")
	_, err = AddCapacity(
		DeclaredCapacity{Nodes: math.MaxInt64},
		DeclaredCapacity{Nodes: 1},
	)
	require.EqualError(t, err, "aggregate Node capacity overflows int64")
	_, err = AddCapacity(
		DeclaredCapacity{GPUs: math.MaxInt64},
		DeclaredCapacity{GPUs: 1},
	)
	require.EqualError(t, err, "aggregate GPU capacity overflows int64")

	inventory := testInventory("inventory", "inventory-uid", "profile", 50_000)
	second := inventory.Spec.RackGroups[0]
	second.ID = "second"
	inventory.Spec.RackGroups = append(inventory.Spec.RackGroups, second)
	require.NoError(t, validateInventoryRackCapacity(inventory), "the aggregate rack boundary remains valid")
	inventory.Spec.RackGroups[1].Count++
	require.EqualError(t, validateInventoryRackCapacity(inventory),
		"desired racks 100001 exceed supported maximum 100000")
}

func TestReconcileRejectsAggregateCapacityBeforeAllocationOrWrites(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, 50_000)
	second := inventory.Spec.RackGroups[0]
	second.ID = "second"
	second.Count = 50_001
	inventory.Spec.RackGroups = append(inventory.Spec.RackGroups, second)
	h := newHarness(t, []runtime.Object{profile, inventory}, nil)
	allocation := NewAllocationCache(h.cache)
	allocationCalls := 0
	allocation.allocate = func(allocate.Input) (allocate.Plan, error) {
		allocationCalls++
		return allocate.Plan{}, nil
	}
	reconciler := NewReconcilerWithAllocationCache(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return false }),
		allocation,
	)
	h.mokka.Fake.ClearActions()

	result, err := reconciler.Reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Equal(t, ReasonCapacityExceeded, result.ValidationReason)
	require.Equal(t, "desired racks 100001 exceed supported maximum 100000", result.ValidationError)
	require.Zero(t, allocationCalls, "rejected capacity must not invoke allocation")
	require.Zero(t, result.Work)
	require.Empty(t, h.mokka.Actions(), "rejected capacity must not issue API writes")
}

func TestReconcileRejectsAdmittedMaximumPerGroupBeforeProfileResolution(t *testing.T) {
	ctx := context.Background()
	inventory := testInventory("inventory", "inventory-uid", "missing-profile", 100_000)
	inventory.Spec.RackGroups = make([]mokkav1alpha1.RackGroup, 64)
	for i := range inventory.Spec.RackGroups {
		inventory.Spec.RackGroups[i] = mokkav1alpha1.RackGroup{
			ID: fmt.Sprintf("group-%d", i), Count: 100_000,
			ProfileRef: mokkav1alpha1.ProfileReference{Name: "missing-profile"},
		}
	}
	h := newHarness(t, []runtime.Object{inventory}, nil)
	allocation := NewAllocationCache(h.cache)
	allocationCalls := 0
	allocation.allocate = func(allocate.Input) (allocate.Plan, error) {
		allocationCalls++
		return allocate.Plan{}, nil
	}
	reconciler := NewReconcilerWithAllocationCache(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return false }),
		allocation,
	)
	h.mokka.Fake.ClearActions()

	result, err := reconciler.Reconcile(ctx, inventory.Name)

	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Equal(t, ReasonCapacityExceeded, result.ValidationReason)
	require.Equal(t, "desired racks 6400000 exceed supported maximum 100000", result.ValidationError)
	require.Zero(t, allocationCalls)
	require.Zero(t, result.Work)
	require.Empty(t, h.mokka.Actions())
}

func TestReconcileGroupIndexesLargeBindingSetOnce(t *testing.T) {
	const (
		rackCount    = 1_000
		nodesPerRack = 100
		nodeCount    = rackCount * nodesPerRack
	)
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, nodesPerRack, 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, rackCount)
	inventory.Finalizers = []string{InventoryFinalizer}

	nodes := make([]*corev1.Node, nodeCount)
	objects := make([]runtime.Object, 0, rackCount+2)
	objects = append(objects, profile, inventory)
	for rackIndex := range rackCount {
		rendered, err := materialize.RenderRack(materialize.RackInput{
			InventoryName: inventory.Name,
			InventoryUID:  inventory.UID,
			Group:         inventory.Spec.RackGroups[0],
			RackIndex:     int32(rackIndex),
			Profile:       profile,
		})
		require.NoError(t, err)
		rack := newRack(inventory, rendered.Name, rendered.Spec)
		rack.UID = types.UID(fmt.Sprintf("rack-uid-%04d", rackIndex))
		rack.ResourceVersion = "1"
		for rackNodeIndex := range nodesPerRack {
			nodeIndex := rackIndex*nodesPerRack + rackNodeIndex
			node := testNode(
				fmt.Sprintf("node-%06d", nodeIndex),
				types.UID(fmt.Sprintf("node-uid-%06d", nodeIndex)),
				int64(nodeIndex),
				map[string]string{"pool": "gpu"},
			)
			nodes[nodeIndex] = node
			rack.Spec.Nodes[rackNodeIndex].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}
		}
		objects = append(objects, rack)
	}

	h := newHarness(t, objects, nodes)
	h.mokka.Fake.ClearActions()
	result, err := h.reconcileGroup(ctx, allocate.GroupKey{
		InventoryName: inventory.Name,
		InventoryUID:  inventory.UID,
		RackGroup:     inventory.Spec.RackGroups[0].ID,
	})
	require.NoError(t, err)
	require.False(t, result.Changed)
	require.Zero(t, result.Work.RacksReconciled, "retained bindings require no Rack reconciliation")
	require.Zero(t, result.Work.AllocationsIndexed)
	require.Zero(t, result.Work.BindingsApplied)
	require.Empty(t, h.mokka.Actions(), "one cached group event must not issue live API calls")

	const changedNode = 54_321
	h.nodes[changedNode].Labels["pool"] = "cpu"
	h.sync(t)
	h.mokka.Fake.ClearActions()
	result, err = h.reconcileGroup(ctx, allocate.GroupKey{
		InventoryName: inventory.Name,
		InventoryUID:  inventory.UID,
		RackGroup:     inventory.Spec.RackGroups[0].ID,
	})
	require.NoError(t, err)
	require.False(t, result.Changed, "cleanup must finish before the binding is removed")
	require.EqualValues(t, 1, result.Work.RacksReconciled)
	require.EqualValues(t, nodesPerRack, result.Work.AllocationsIndexed)
	require.EqualValues(t, nodesPerRack-1, result.Work.BindingsApplied)
	require.Len(t, result.CleanupNeeded, 1)
	require.EqualValues(t, changedNode/nodesPerRack, result.CleanupNeeded[0].Binding.Coordinate.RackIndex)
	require.Empty(t, h.mokka.Actions(), "pending cleanup must leave the exact Rack unchanged")
}

func TestGroupAllocationIndexesOnlyChangedRackAmong100K(t *testing.T) {
	group := allocate.GroupKey{InventoryName: "inventory", InventoryUID: "inventory-uid", RackGroup: "group"}
	const changed = int32(54_321)
	plan := allocate.Plan{Retained: make([]allocate.Binding, 0, 99_999)}
	for rackIndex := int32(0); rackIndex < 100_000; rackIndex++ {
		binding := allocate.Binding{
			Coordinate: allocate.Coordinate{Group: group, RackIndex: rackIndex},
			Node:       allocate.NodeReference{Name: fmt.Sprintf("node-%06d", rackIndex), UID: types.UID(fmt.Sprintf("uid-%06d", rackIndex))},
		}
		if rackIndex == changed {
			plan.Released = append(plan.Released, allocate.Release{
				Binding: binding, Reason: allocate.ReleaseSelectorMismatch,
			})
			continue
		}
		plan.Retained = append(plan.Retained, binding)
	}

	indices := allocationChangedRacks(plan, group, 100_000)
	require.Equal(t, []int32{changed}, indices)
	indexed := indexGroupAllocation(plan, group, indices)
	require.EqualValues(t, 1, indexed.indexed)
	require.Len(t, indexed.racks, 1)
	require.Len(t, indexed.releases, 1)
}

func TestReconcileReportsOverlapAndRetainsLastGoodRackForMissingProfile(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	inventory.Finalizers = []string{InventoryFinalizer}
	other := testInventory("other", "other-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory, other}, []*corev1.Node{node})

	result, err := h.reconcileGroup(ctx, allocate.GroupKey{
		InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: "group",
	})
	require.NoError(t, err)
	require.Len(t, result.Allocation.Conflicts, 1)
	require.Equal(t, allocate.ConflictSelectorOverlap, result.Allocation.Conflicts[0].Kind)
	require.Empty(t, result.Allocation.Assigned)

	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpurackprofiles"), "", profile.Name))
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
	require.Equal(t, node.UID, got.Spec.Nodes[0].NodeRef.UID)
}

func TestReconcileInvalidInventoryAndProfileRetainLastGoodRacks(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	invalidProfile, err := h.mokka.MokkaV1alpha1().SGPURackProfiles().Get(ctx, profile.Name, metav1.GetOptions{})
	require.NoError(t, err)
	invalidProfile.Spec.Node.Topology.GPUSlots[0].PCIAddress = "not-a-pci-address"
	_, err = h.mokka.MokkaV1alpha1().SGPURackProfiles().Update(ctx, invalidProfile, metav1.UpdateOptions{})
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
	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpurackprofiles"), "", profile.Name))
	require.NoError(t, h.mokka.Tracker().Add(validProfile))
	invalidInventory, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	invalidInventory.Spec.RackGroups[0].Count = 0
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

func TestReconcileProjectedLabelSelectorRetainsBindingWithoutWrites(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)

	invalid, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	invalid.Spec.RackGroups[0].Placement.NodeSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key: metadata.CliqueLabel, Operator: metav1.LabelSelectorOpExists,
	}}}
	invalid.Generation++
	_, err = h.mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, invalid, metav1.UpdateOptions{})
	require.NoError(t, err)
	h.sync(t)
	h.mokka.Fake.ClearActions()

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Equal(t,
		`rack group "group" selector: selector must not reference controller-owned label "`+metadata.CliqueLabel+`"`,
		result.ValidationError,
	)
	require.Empty(t, result.CleanupNeeded)
	require.Empty(t, h.mokka.Actions(), "invalid selectors must not mutate or clean up last-good racks")

	retained, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, retained.Spec.Nodes[0].NodeRef)
	require.Equal(t, node.UID, retained.Spec.Nodes[0].NodeRef.UID)
}

func TestReconcileReleasesTerminatingNodeAsIneligible(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)

	now := metav1.Now()
	h.nodes[0].DeletionTimestamp = &now
	h.sync(t)
	result, err := h.reconcile(ctx, inventory.Name)

	require.NoError(t, err)
	require.Empty(t, result.Allocation.Retained)
	require.Empty(t, result.Allocation.Assigned)
	require.Len(t, result.Allocation.Released, 1)
	require.Equal(t, allocate.ReleaseNodeIneligible, result.Allocation.Released[0].Reason)
	require.Len(t, result.CleanupNeeded, 1)
	require.Equal(t, CleanupNodeIneligible, result.CleanupNeeded[0].Reason)
}

func TestReconcileUsesCurrentInventoryOverStaleListSnapshot(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)

	current := inventory.DeepCopy()
	current.Finalizers = []string{InventoryFinalizer}
	current.Spec.RackGroups[0].Placement.NodeSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key: metadata.AssignedLabel, Operator: metav1.LabelSelectorOpExists,
	}}}
	h.mokka.Fake.ClearActions()
	reconciler := NewReconciler(
		&inventoryOverrideCache{Cache: h.cache, inventory: current},
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return false }),
	)

	result, err := reconciler.Reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Empty(t, result.CleanupNeeded)
	require.Empty(t, h.mokka.Actions(), "a stale inventory list must not release current last-good bindings")
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
	require.NoError(t, h.mokka.Tracker().Delete(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpurackprofiles"), "", profile.Name))
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
	require.Equal(t, "0000:03:00.0", got.Spec.Nodes[0].GPUs[1].PCIAddress)
	require.Equal(t, node.UID, got.Spec.Nodes[0].NodeRef.UID)
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

	updatedProfile, err := h.mokka.MokkaV1alpha1().SGPURackProfiles().Get(ctx, profile.Name, metav1.GetOptions{})
	require.NoError(t, err)
	updatedProfile.Spec.Rack.NodesPerRack = 1
	updatedProfile.Spec.Node.Topology.GPUFabric.Domain.GPUCount = 1
	updatedProfile.Generation++
	_, err = h.mokka.MokkaV1alpha1().SGPURackProfiles().Update(ctx, updatedProfile, metav1.UpdateOptions{})
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
	require.Len(t, surviving.Spec.Nodes, 2, "a bound high slot remains until projection cleanup")
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
	require.Len(t, shrunk.Spec.Nodes, 1)
	_, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, retiringName, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestReconcileDoesNotGrowRackWhileRetiredCapacityAwaitsCleanup(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, 2)
	nodes := []*corev1.Node{
		testNode("one", "one-uid", 1, map[string]string{"pool": "gpu"}),
		testNode("two", "two-uid", 2, map[string]string{"pool": "gpu"}),
	}
	h := newHarness(t, []runtime.Object{profile, inventory}, nodes)
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	updatedProfile, err := h.mokka.MokkaV1alpha1().SGPURackProfiles().Get(ctx, profile.Name, metav1.GetOptions{})
	require.NoError(t, err)
	updatedProfile.Spec.Rack.NodesPerRack = 2
	updatedProfile.Spec.Node.Topology.GPUFabric.Domain.GPUCount = 2
	updatedProfile.Generation++
	_, err = h.mokka.MokkaV1alpha1().SGPURackProfiles().Update(ctx, updatedProfile, metav1.UpdateOptions{})
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
	require.Len(t, result.CleanupNeeded, 1)
	require.Equal(t, CleanupCapacityShrink, result.CleanupNeeded[0].Reason)
	surviving, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Len(t, surviving.Spec.Nodes, 1,
		"the surviving rack must not grow until the retiring rack releases its durable Node capacity")
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
	require.NoError(t, h.mokka.Tracker().Update(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpurackprofiles"), profile, ""))
	h.sync(t)

	conflicted := false
	h.mokka.PrependReactor("patch", "sgpuracks", func(_ k8stesting.Action) (bool, runtime.Object, error) {
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

func TestReconcileSurfacesRackFieldOwnershipConflict(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	h := newHarness(t, []runtime.Object{profile, inventory}, nil)
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)

	profile.Generation++
	profile.Spec.Software.DriverVersion = "new"
	require.NoError(t, h.mokka.Tracker().Update(
		mokkav1alpha1.SchemeGroupVersion.WithResource("sgpurackprofiles"), profile, "",
	))
	h.sync(t)
	h.mokka.PrependReactor("patch", "sgpuracks", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewApplyConflict([]metav1.StatusCause{{
			Type: metav1.CauseTypeFieldManagerConflict, Field: ".spec.profileRef.revision",
		}}, "owned by another manager")
	})

	result, err := h.reconcile(ctx, inventory.Name)
	require.Error(t, err)
	require.True(t, apierrors.IsConflict(err))
	var ownershipErr *OwnershipConflictError
	require.ErrorAs(t, err, &ownershipErr)
	require.Equal(t, []OwnershipConflict{ownershipErr.Conflict}, result.OwnershipConflicts)
	require.Equal(t, materialize.RackName(inventory.Name, inventory.UID, "group", 0), ownershipErr.Conflict.RackName)
}

func TestReconcileClassifiesInvalidRackCreateAsProfileIssue(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, 2)
	h := newHarness(t, []runtime.Object{profile, inventory}, nil)
	creates := 0
	h.mokka.PrependReactor("create", "sgpuracks", func(k8stesting.Action) (bool, runtime.Object, error) {
		creates++
		return true, nil, apierrors.NewInvalid(
			schema.GroupKind{Group: mokkav1alpha1.GroupName, Kind: "SGPURack"},
			"rack",
			nil,
		)
	})

	result, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.False(t, result.ResolvedRefs)
	require.Equal(t, []ProfileIssue{{
		RackGroup: "group", ProfileName: profile.Name,
		Reason: fmt.Sprintf(
			"rack %q was rejected by API validation",
			materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		),
	}}, result.ProfileIssues)
	require.Equal(t, 1, creates, "one invalid rack must stop further creates for the affected profile group")
}

func TestReconcileLeavesTransientRackCreateErrorsRetryable(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, 1)
	h := newHarness(t, []runtime.Object{profile, inventory}, nil)
	h.mokka.PrependReactor("create", "sgpuracks", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("apiserver unavailable")
	})

	result, err := h.reconcile(ctx, inventory.Name)
	require.Error(t, err)
	require.True(t, apierrors.IsServiceUnavailable(err))
	require.True(t, result.ResolvedRefs)
	require.Empty(t, result.ProfileIssues)
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
	require.Equal(t, old.UID, got.Spec.Nodes[0].NodeRef.UID)

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
	require.Equal(t, replacement.UID, got.Spec.Nodes[0].NodeRef.UID)
}

func TestReconcileRetriesAllocationWhenStaleRackApplyRecreatesObject(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	old := testNode("same", "old-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{old})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	h.sync(t)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "group", 0)
	stale, err := h.cache.Rack(rackName)
	require.NoError(t, err)
	require.NoError(t, h.mokka.MokkaV1alpha1().SGPURacks().Delete(ctx, rackName, metav1.DeleteOptions{}))
	replacement := testNode("same", "new-uid", 2, map[string]string{"pool": "gpu"})
	reconciler := NewReconciler(
		&nodeOverrideCache{Cache: h.cache, nodes: []*corev1.Node{replacement}},
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return true }),
	)

	_, err = reconciler.Reconcile(ctx, inventory.Name)
	require.Error(t, err)
	require.True(t, apierrors.IsConflict(err))
	recreated, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rackName, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotEqual(t, stale.UID, recreated.UID)
	require.Nil(t, recreated.Spec.Nodes[0].NodeRef,
		"the replacement remains pending while the allocation snapshot contains the stale binding")

	h.nodes = []*corev1.Node{replacement}
	h.sync(t)
	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	recreated, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rackName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, replacement.UID, recreated.Spec.Nodes[0].NodeRef.UID)
}

func TestCleanupAcknowledgementSurvivesConflictAndStaleCache(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("p", "profile-uid", 1, 1, 1)
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	node := testNode("node", "node-uid", 1, map[string]string{"pool": "gpu"})
	h := newHarness(t, []runtime.Object{profile, inventory}, []*corev1.Node{node})
	_, err := h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)

	h.nodes = nil
	h.sync(t)
	gate := &testCleanupGate{ready: true}
	reconciler := NewReconciler(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		gate,
	)
	conflicted := false
	h.mokka.PrependReactor("patch", "sgpuracks", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicted {
			return false, nil, nil
		}
		conflicted = true
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Group: mokkav1alpha1.GroupName, Resource: "sgpuracks"},
			materialize.RackName(inventory.Name, inventory.UID, "group", 0),
			errors.New("test conflict"),
		)
	})

	_, err = reconciler.Reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	require.True(t, conflicted)
	require.True(t, gate.Ready(CleanupNeeded{}), "cleanup must remain acknowledged until the cache observes the removed binding")
	stored, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(
		ctx,
		materialize.RackName(inventory.Name, inventory.UID, "group", 0),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Nil(t, stored.Spec.Nodes[0].NodeRef)

	_, err = reconciler.Reconcile(ctx, inventory.Name)
	require.Error(t, err)
	require.True(t, apierrors.IsConflict(err))
	require.True(t, gate.Ready(CleanupNeeded{}), "a stale-cache conflict must not discard the acknowledgement")
	h.sync(t)
	reconciler = NewReconciler(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		gate,
	)
	_, err = reconciler.Reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	stored, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, stored.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Nil(t, stored.Spec.Nodes[0].NodeRef, "a stale cache reconcile must not restore a cleaned binding")
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

func TestReconcileInventoryDeletionRetiresLiveCacheMissingRackBeforeFinalizer(t *testing.T) {
	ctx := context.Background()
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	inventory.Finalizers = []string{InventoryFinalizer}
	now := metav1.Now()
	inventory.DeletionTimestamp = &now
	h := newHarness(t, []runtime.Object{inventory}, nil)

	liveRack := newRack(inventory, "cache-missing", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
		Nodes: []mokkav1alpha1.SGPURackNode{{
			Index:   0,
			NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"},
		}},
	})
	liveRack, err := h.mokka.MokkaV1alpha1().SGPURacks().Create(
		ctx, liveRack, metav1.CreateOptions{FieldManager: RackFieldManager},
	)
	require.NoError(t, err)

	foreignInventory := inventory.DeepCopy()
	foreignInventory.UID = "replacement-uid"
	foreignRack := newRack(foreignInventory, "foreign", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: foreignInventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 1},
		Nodes:        []mokkav1alpha1.SGPURackNode{{Index: 0}},
	})
	foreignRack, err = h.mokka.MokkaV1alpha1().SGPURacks().Create(
		ctx, foreignRack, metav1.CreateOptions{FieldManager: RackFieldManager},
	)
	require.NoError(t, err)
	h.mokka.Fake.ClearActions()

	result, err := h.reconcile(ctx, inventory.Name)
	require.ErrorIs(t, err, ErrRackCacheStale)
	require.Len(t, result.CleanupNeeded, 1)
	require.Len(t, h.mokka.Actions(), 1)
	liveList := h.mokka.Actions()[0].(k8stesting.ListAction)
	require.Equal(t, InventoryNameLabel+"=inventory", liveList.GetListRestrictions().Labels.String())
	retained, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, retained.Finalizers, InventoryFinalizer)
	retiring, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, liveRack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, retiring.Finalizers, RackFinalizer)
	require.NotNil(t, retiring.Spec.Nodes[0].NodeRef)

	h.cleaned = true
	_, err = h.reconcile(ctx, inventory.Name)
	require.ErrorIs(t, err, ErrRackCacheStale)
	_, err = h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, liveRack.Name, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
	retained, err = h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, retained.Finalizers, InventoryFinalizer)

	_, err = h.reconcile(ctx, inventory.Name)
	require.NoError(t, err)
	finished, err := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotContains(t, finished.Finalizers, InventoryFinalizer)
	untouched, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, foreignRack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, untouched.Finalizers, RackFinalizer)
}

func TestReconcileInventoryDeletionPropagatesLiveListError(t *testing.T) {
	ctx := context.Background()
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	inventory.Finalizers = []string{InventoryFinalizer}
	now := metav1.Now()
	inventory.DeletionTimestamp = &now
	h := newHarness(t, []runtime.Object{inventory}, nil)

	wantErr := errors.New("list failed")
	h.mokka.PrependReactor("list", "sgpuracks", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, wantErr
	})
	_, err := h.reconcile(ctx, inventory.Name)
	require.ErrorIs(t, err, wantErr)
	retained, getErr := h.mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, getErr)
	require.Contains(t, retained.Finalizers, InventoryFinalizer)
}

func TestInformerIndexesExposeOnlyDirectDependents(t *testing.T) {
	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, InventoryIndexers())
	inventory := testInventory("inventory", "inventory-uid", "p", 1)
	inventory.Spec.RackGroups = append(inventory.Spec.RackGroups, mokkav1alpha1.RackGroup{
		ID: "second", ProfileRef: mokkav1alpha1.ProfileReference{Name: "p"},
	})
	require.NoError(t, inventoryIndexer.Add(inventory))
	byProfile, err := inventoryIndexer.ByIndex(InventoryByProfileNameIndex, "p")
	require.NoError(t, err)
	require.Equal(t, []any{inventory}, byProfile)

	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, Indexers())
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
			Nodes: []mokkav1alpha1.SGPURackNode{
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
	t           *testing.T
	mokka       *mokkafake.Clientset
	nodes       []*corev1.Node
	cache       *listerCache
	cleaned     bool
	nextRackUID int64
}

type testCleanupGate struct {
	ready bool
}

func (g *testCleanupGate) Ready(CleanupNeeded) bool { return g.ready }

type inventoryOverrideCache struct {
	Cache
	inventory *mokkav1alpha1.SGPUInventory
}

func (c *inventoryOverrideCache) Inventory(name string) (*mokkav1alpha1.SGPUInventory, error) {
	if name == c.inventory.Name {
		return c.inventory, nil
	}
	return c.Cache.Inventory(name)
}

type nodeOverrideCache struct {
	Cache
	nodes []*corev1.Node
}

func (c *nodeOverrideCache) AllocationNodes() ([]allocate.Node, error) {
	nodes := make([]allocate.Node, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, allocate.Node{
			Name: node.Name, UID: node.UID,
			CreationTimestamp: node.CreationTimestamp.Time,
			Terminating:       node.DeletionTimestamp != nil,
			Labels:            node.Labels,
		})
	}
	return nodes, nil
}

func newHarness(t *testing.T, mokkaObjects []runtime.Object, nodes []*corev1.Node) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		mokka: mokkafake.NewSimpleClientset(mokkaObjects...),
		nodes: nodes,
	}
	h.installRackCreateReactor()
	h.installRackApplyReactor()
	h.sync(t)
	return h
}

func (h *harness) installRackCreateReactor() {
	h.mokka.PrependReactor("create", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create := action.(k8stesting.CreateActionImpl)
		require.Equal(h.t, RackFieldManager, create.GetCreateOptions().FieldManager)
		desired := create.GetObject().(*mokkav1alpha1.SGPURack).DeepCopy()
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		if _, err := h.mokka.Tracker().Get(resource, "", desired.Name); err == nil {
			return true, nil, apierrors.NewAlreadyExists(mokkav1alpha1.Resource("sgpuracks"), desired.Name)
		} else if !apierrors.IsNotFound(err) {
			return true, nil, err
		}
		h.nextRackUID++
		desired.UID = types.UID(fmt.Sprintf("uid-%s-%d", desired.Name, h.nextRackUID))
		desired.ResourceVersion = "1"
		desired.ManagedFields = []metav1.ManagedFieldsEntry{{
			Manager: RackFieldManager, Operation: metav1.ManagedFieldsOperationUpdate,
		}}
		err := h.mokka.Tracker().Create(resource, desired, "")
		return true, desired, err
	})
}

//nolint:cyclop // The fake reactor models the API server's create/update/SSA conflict cases.
func (h *harness) installRackApplyReactor() {
	h.mokka.PrependReactor("patch", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchActionImpl)
		require.Equal(h.t, types.ApplyPatchType, patch.GetPatchType())
		require.Equal(h.t, RackFieldManager, patch.GetPatchOptions().FieldManager)
		require.NotNil(h.t, patch.GetPatchOptions().Force)
		require.False(h.t, *patch.GetPatchOptions().Force)

		desired := &mokkav1alpha1.SGPURack{}
		require.NoError(h.t, json.Unmarshal(patch.GetPatch(), desired))
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		stored, err := h.mokka.Tracker().Get(resource, "", patch.GetName())
		if apierrors.IsNotFound(err) {
			h.nextRackUID++
			desired.UID = types.UID(fmt.Sprintf("uid-%s-%d", desired.Name, h.nextRackUID))
			desired.ResourceVersion = "1"
			err = h.mokka.Tracker().Create(resource, desired, "")
			return true, desired, err
		}
		if err != nil {
			return true, nil, err
		}
		current := stored.(*mokkav1alpha1.SGPURack).DeepCopy()
		if desired.ResourceVersion != current.ResourceVersion {
			return true, nil, apierrors.NewConflict(
				mokkav1alpha1.Resource("sgpuracks"), desired.Name, errors.New("resource version changed"),
			)
		}
		current.Spec = desired.Spec
		for _, key := range []string{InventoryNameLabel, RackGroupLabel, RackIndexLabel} {
			delete(current.Labels, key)
			if value, found := desired.Labels[key]; found {
				if current.Labels == nil {
					current.Labels = make(map[string]string)
				}
				current.Labels[key] = value
			}
		}
		delete(current.Annotations, InventoryUIDAnnotation)
		if value, found := desired.Annotations[InventoryUIDAnnotation]; found {
			if current.Annotations == nil {
				current.Annotations = make(map[string]string)
			}
			current.Annotations[InventoryUIDAnnotation] = value
		}
		current.Finalizers = removeString(current.Finalizers, RackFinalizer)
		if slices.Contains(desired.Finalizers, RackFinalizer) {
			current.Finalizers = append(current.Finalizers, RackFinalizer)
		}
		current.OwnerReferences = slices.DeleteFunc(current.OwnerReferences, func(owner metav1.OwnerReference) bool {
			return owner.Controller != nil && *owner.Controller && owner.APIVersion == mokkav1alpha1.SchemeGroupVersion.String() && owner.Kind == "SGPUInventory"
		})
		if owner := controllerInventoryOwner(desired); owner != nil {
			current.OwnerReferences = append(current.OwnerReferences, *owner)
		}
		current.ResourceVersion += "a"
		err = h.mokka.Tracker().Update(resource, current, "")
		return true, current, err
	})
}

func (h *harness) sync(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, InventoryIndexers())
	profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, Indexers())
	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	inventories, err := h.mokka.MokkaV1alpha1().SGPUInventories().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	for i := range inventories.Items {
		require.NoError(t, inventoryIndexer.Add(inventories.Items[i].DeepCopy()))
	}
	profiles, err := h.mokka.MokkaV1alpha1().SGPURackProfiles().List(ctx, metav1.ListOptions{})
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

	h.cache = newListerCache(
		mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
		mokkalisters.NewSGPURackProfileLister(profileIndexer),
		rackIndexer,
		newNodeLister(nodeIndexer),
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

func (h *harness) reconcileGroup(ctx context.Context, key allocate.GroupKey) (Result, error) {
	reconciler := NewReconciler(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return h.cleaned }),
	)
	return reconciler.ReconcileGroup(ctx, key)
}

func testInventory(name string, uid types.UID, profileName string, count int32) *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		TypeMeta:   metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid, Generation: 1},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
			ID: "group", Count: count,
			ProfileRef: mokkav1alpha1.ProfileReference{Name: profileName},
			Placement: &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "gpu"},
			}},
		}}},
	}
}

func testProfile(name string, uid types.UID, generation int64, nodesPerRack, gpuCount int32) *mokkav1alpha1.SGPURackProfile {
	gpuSlots := make([]mokkav1alpha1.GPUSlot, gpuCount)
	for i := range gpuSlots {
		gpuSlots[i] = mokkav1alpha1.GPUSlot{
			Index: int32(i), PCIAddress: "0000:0" + string(rune('1'+i)) + ":00.0",
			RootComplex: "pci0000:00", NumaNode: int32(i), HostProcessorIndex: int32(i),
		}
	}
	return &mokkav1alpha1.SGPURackProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPURackProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid, Generation: generation},
		Spec: mokkav1alpha1.SGPURackProfileSpec{
			Rack: mokkav1alpha1.SGPURackShape{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUNode{
				GPUs: mokkav1alpha1.SGPUGPUs{Count: gpuCount},
				Topology: &mokkav1alpha1.SGPUTopology{
					GPUSlots: gpuSlots,
					GPUFabric: &mokkav1alpha1.GPUFabric{
						Type: "NVLink", Generation: 5, LinksPerGPU: 18, BandwidthPerLinkMBps: 50000,
						Domain: &mokkav1alpha1.FabricDomain{Scope: "Rack", GPUCount: nodesPerRack * gpuCount},
					},
				},
			},
			Software: &mokkav1alpha1.SGPUSoftware{DriverVersion: "driver"},
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
