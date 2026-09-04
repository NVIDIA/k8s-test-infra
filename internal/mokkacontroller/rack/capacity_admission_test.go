// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clienttesting "k8s.io/client-go/testing"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
)

func TestCapacityAdmissionIsDeterministicAndRecoversCapacity(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 60_000, 1)
	blocked := admissionTestInventory("blocked", "blocked-uid", profile.Name, 50_000, 2)
	later := admissionTestInventory("later", "later-uid", profile.Name, 40_000, 3)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{later, blocked, first},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}

	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, first))
	require.False(t, capacityAdmissionDecision(t, admission, revision, blocked))
	require.True(t, capacityAdmissionDecision(t, admission, revision, later),
		"first-fit admission uses capacity that an older oversized declaration cannot consume")
	require.EqualValues(t, 1, admission.computations.Load())
	metadataOnly := first.DeepCopy()
	metadataOnly.ResourceVersion = "2"
	require.True(t, capacityAdmissionDecision(t, admission, revision, metadataOnly),
		"metadata-only informer updates must keep using the capacity snapshot")

	restarted := NewCapacityAdmission(source)
	require.True(t, capacityAdmissionDecision(t, restarted, restarted.currentRevision(), first))
	require.False(t, capacityAdmissionDecision(t, restarted, restarted.currentRevision(), blocked))
	require.True(t, capacityAdmissionDecision(t, restarted, restarted.currentRevision(), later))

	source.mu.Lock()
	source.inventories = []*mokkav1alpha1.SGPUInventory{blocked, later}
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, blocked))
	require.True(t, capacityAdmissionDecision(t, admission, revision, later))

	resized := blocked.DeepCopy()
	resized.ResourceVersion = "2"
	resized.Spec.RackGroups[0].Count = 100_000
	source.mu.Lock()
	source.inventories = []*mokkav1alpha1.SGPUInventory{resized, later}
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, resized))
	require.False(t, capacityAdmissionDecision(t, admission, revision, later))
}

func TestCapacityAdmissionWakesRejectedCandidateAfterDurableCapacityRelease(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1_000, 1)
	candidate := admissionTestInventory("candidate", "candidate-uid", profile.Name, 60, 2)
	retired := admissionTestInventory("retired", "retired-uid", profile.Name, 50, 1)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{candidate},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	for index := range 50 {
		source.racks = append(source.racks, admissionRack(retired, int32(index), 1_000))
	}

	allocation := NewAllocationCache(source)
	admission := allocation.admission
	revision := admission.currentRevision()
	require.False(t, capacityAdmissionDecision(t, admission, revision, candidate))
	require.Equal(t, candidate.Name, allocation.CapacityWakeup(),
		"a rejected candidate must remain as the bounded recomputation trigger")

	source.mu.Lock()
	source.racks = nil
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, candidate))
	require.Equal(t, []string{candidate.Name}, allocation.CapacityTransitions(),
		"the capacity release must publish the rejected-to-ready transition")
}

func TestCapacityAdmissionReportsSameTimestampAddDisplacement(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	incumbent := admissionTestInventory("z-incumbent", "incumbent-uid", profile.Name, 60_000, 1)
	newcomer := admissionTestInventory("a-newcomer", "newcomer-uid", profile.Name, 60_000, 1)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{incumbent},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}

	allocation := NewAllocationCache(source)
	admission := allocation.admission
	revision := admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, incumbent))

	source.mu.Lock()
	source.inventories = append(source.inventories, newcomer)
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, newcomer))
	require.Equal(t, []string{newcomer.Name, incumbent.Name}, allocation.CapacityTransitions(),
		"name and UID tie-breaking can admit the newcomer and must wake the displaced incumbent")
}

func TestCapacityAdmissionReportsSameTimestampRackGroupDisplacement(t *testing.T) {
	incumbent := admissionTestInventory("z-incumbent", "incumbent-uid", "missing", 1, 1)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{incumbent},
	}

	allocation := NewAllocationCache(source)
	admission := allocation.admission
	revision := admission.currentRevision()
	decision, err := admission.decision(revision, incumbent, DeclaredCapacity{})
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionAccepted, decision)

	newcomer := admissionTestInventory("a-newcomer", "newcomer-uid", "missing", 1, 1)
	expandAdmissionRackGroups(newcomer, MaxRackGroups)
	source.mu.Lock()
	source.inventories = append(source.inventories, newcomer)
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	decision, err = admission.decision(revision, newcomer, DeclaredCapacity{})
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionAccepted, decision)
	decision, err = admission.decision(revision, incumbent, DeclaredCapacity{})
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionRejected, decision)
	require.Equal(t, []string{newcomer.Name, incumbent.Name}, allocation.CapacityTransitions(),
		"rack-group admission displacement must wake a previously accepted wholly unresolved Inventory")
}

func TestCapacityWakeupRejectsStaleInventoryIdentity(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	original := admissionTestInventory("inventory", "original-uid", profile.Name, 1, 1)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{original},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	allocation := NewAllocationCache(source)
	require.True(t, capacityAdmissionDecision(
		t, allocation.admission, allocation.admission.currentRevision(), original,
	))

	replacement := original.DeepCopy()
	replacement.UID = "replacement-uid"
	source.mu.Lock()
	source.inventories = []*mokkav1alpha1.SGPUInventory{replacement}
	source.mu.Unlock()

	require.Empty(t, allocation.CapacityWakeup(),
		"a stale snapshot must not route a same-name replacement as its recomputation seed")
}

func TestCapacityAdmissionEnforcesAggregateRackGroupLimit(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 1, 1)
	expandAdmissionRackGroups(first, MaxRackGroups)
	blocked := admissionTestInventory("blocked", "blocked-uid", profile.Name, 1, 2)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{blocked, first},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}

	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, first))
	require.False(t, capacityAdmissionDecision(t, admission, revision, blocked))
	input, err := allocationInputRevision(source, admission, revision)
	require.NoError(t, err)
	require.Len(t, input.Groups, MaxRackGroups)

	source.mu.Lock()
	source.inventories = []*mokkav1alpha1.SGPUInventory{blocked}
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	require.True(t, capacityAdmissionDecision(t, admission, revision, blocked),
		"removing the older declaration must release the rack-group budget")
}

func TestCapacityAdmissionKeepsDurableRacksChargedUntilRejectedInventoryRetires(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1_000, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 100, 1)
	rejected := admissionTestInventory("rejected", "rejected-uid", profile.Name, 40, 2)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{rejected, first},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	for index := range 60 {
		source.racks = append(source.racks, admissionRack(first, int32(index), 1_000))
	}
	for index := range 40 {
		source.racks = append(source.racks, admissionRack(rejected, int32(index), 1_000))
	}

	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()
	firstCapacity, materializes, err := materializedInventoryCapacity(source, first)
	require.NoError(t, err)
	require.True(t, materializes)
	decision, err := admission.decision(revision, first, firstCapacity)
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionPending, decision,
		"the older inventory must not grow while the rejected topology is still live")
	require.Equal(t, []inventoryInstance{{name: first.Name, uid: first.UID}}, admission.waiters())

	rejectedCapacity, materializes, err := materializedInventoryCapacity(source, rejected)
	require.NoError(t, err)
	require.True(t, materializes)
	decision, err = admission.decision(revision, rejected, rejectedCapacity)
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionRejected, decision)

	source.mu.Lock()
	source.racks = source.racks[:60]
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	decision, err = admission.decision(revision, first, firstCapacity)
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionAccepted, decision,
		"capacity becomes available only after the durable rejected racks disappear")
}

func TestCapacityAdmissionTracksEveryCandidateMadeReadyByOneCapacityChange(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1_000, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 50, 1)
	second := admissionTestInventory("second", "second-uid", profile.Name, 50, 2)
	retiring := admissionTestInventory("retiring", "retiring-uid", profile.Name, 60, 3)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{retiring, second, first},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	for index := range 20 {
		source.racks = append(source.racks, admissionRack(first, int32(index), 1_000))
		source.racks = append(source.racks, admissionRack(second, int32(index), 1_000))
	}
	for index := range 60 {
		source.racks = append(source.racks, admissionRack(retiring, int32(index), 1_000))
	}

	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()
	for _, inventory := range []*mokkav1alpha1.SGPUInventory{first, second} {
		capacity, materializes, err := materializedInventoryCapacity(source, inventory)
		require.NoError(t, err)
		require.True(t, materializes)
		decision, err := admission.decision(revision, inventory, capacity)
		require.NoError(t, err)
		require.Equal(t, capacityAdmissionPending, decision)
	}
	require.Equal(t, []inventoryInstance{
		{name: first.Name, uid: first.UID},
		{name: second.Name, uid: second.UID},
	}, admission.waiters())

	source.mu.Lock()
	remaining := source.racks[:0]
	for _, rack := range source.racks {
		if rack.Spec.InventoryRef.UID != retiring.UID {
			remaining = append(remaining, rack)
		}
	}
	source.racks = remaining
	source.mu.Unlock()
	admission.Invalidate()
	revision = admission.currentRevision()
	for _, inventory := range []*mokkav1alpha1.SGPUInventory{first, second} {
		capacity, materializes, err := materializedInventoryCapacity(source, inventory)
		require.NoError(t, err)
		require.True(t, materializes)
		decision, err := admission.decision(revision, inventory, capacity)
		require.NoError(t, err)
		require.Equal(t, capacityAdmissionAccepted, decision)
	}
}

func TestCapacityAdmissionChargesLastGoodRacksForInvalidInventory(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1_000, 1)
	invalid := admissionTestInventory("invalid", "invalid-uid", "missing", 60, 1)
	follower := admissionTestInventory("follower", "follower-uid", profile.Name, 50, 2)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{invalid, follower},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	for index := range 60 {
		source.racks = append(source.racks, admissionRack(invalid, int32(index), 1_000))
	}

	admission := NewCapacityAdmission(source)
	capacity, materializes, err := materializedInventoryCapacity(source, follower)
	require.NoError(t, err)
	require.True(t, materializes)
	decision, err := admission.decision(admission.currentRevision(), follower, capacity)
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionRejected, decision,
		"last-good topology retained for an unresolved inventory still consumes aggregate capacity")
}

func TestCapacityRejectedReconcileRetiresMaterializedRacks(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 100_000, 1)
	rejected := admissionTestInventory("rejected", "rejected-uid", profile.Name, 1, 2)
	rejected.Finalizers = []string{InventoryFinalizer}
	rack := admissionRack(rejected, 0, 1)
	rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}
	h := newHarness(t, []runtime.Object{profile, first, rejected, rack}, nil)
	h.mokka.Fake.ClearActions()

	result, err := h.reconcile(ctx, rejected.Name)

	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Equal(t, ReasonCapacityExceeded, result.ValidationReason)
	require.Len(t, result.CleanupNeeded, 1)
	require.Equal(t, CleanupCapacityRejected, result.CleanupNeeded[0].Reason)
	stored, err := h.mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, types.UID("node-uid"), stored.Spec.Nodes[0].NodeRef.UID,
		"rack retirement must wait for exact projection cleanup")
}

func TestRackGroupCapacityRejectedReconcileRetiresWhollyUnresolvedLastGoodRack(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	incumbent := admissionTestInventory("incumbent", "incumbent-uid", profile.Name, 1, 1)
	expandAdmissionRackGroups(incumbent, MaxRackGroups)
	rejected := admissionTestInventory("rejected", "rejected-uid", "missing", 1, 2)
	rejected.Finalizers = []string{InventoryFinalizer}
	rack := admissionRack(rejected, 0, 1)
	rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}
	h := newHarness(t, []runtime.Object{profile, incumbent, rejected, rack}, nil)
	h.mokka.Fake.ClearActions()

	result, err := h.reconcile(ctx, rejected.Name)

	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Equal(t, ReasonCapacityExceeded, result.ValidationReason)
	require.Len(t, result.CleanupNeeded, 1)
	require.Equal(t, CleanupCapacityRejected, result.CleanupNeeded[0].Reason)
}

func TestRackGroupAdmissionAcceptsWhollyUnresolvedInventoryWithinBudget(t *testing.T) {
	ctx := context.Background()
	inventory := admissionTestInventory("inventory", "inventory-uid", "missing", 1, 1)
	inventory.Finalizers = []string{InventoryFinalizer}
	rack := admissionRack(inventory, 0, 1)
	rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}
	h := newHarness(t, []runtime.Object{inventory, rack}, nil)
	h.mokka.Fake.ClearActions()

	result, err := h.reconcile(ctx, inventory.Name)

	require.NoError(t, err)
	require.True(t, result.Accepted)
	require.False(t, result.ResolvedRefs)
	require.Empty(t, result.CleanupNeeded)
	require.Empty(t, h.mokka.Actions(), "rack-group-admitted unresolved Inventories must preserve last-good racks")
}

func TestCapacityAdmissionIgnoresInvalidAndWhollyUnresolvedInventories(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	invalidProfile := testProfile("invalid", "invalid-profile-uid", 1, 0, 1)
	invalidSpec := admissionTestInventory("invalid-spec", "invalid-spec-uid", profile.Name, 100_000, 1)
	invalidSpec.Spec.RackGroups[0].Count = 0
	missingProfile := admissionTestInventory("missing", "missing-uid", "missing", 100_000, 2)
	badProfile := admissionTestInventory("bad-profile", "bad-profile-uid", invalidProfile.Name, 100_000, 3)
	valid := admissionTestInventory("valid", "valid-uid", profile.Name, 100_000, 4)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{invalidSpec, missingProfile, badProfile, valid},
		profiles: map[string]*mokkav1alpha1.SGPURackProfile{
			profile.Name: profile, invalidProfile.Name: invalidProfile,
		},
	}
	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()

	require.True(t, capacityAdmissionDecision(t, admission, revision, valid))
	snapshot := admission.snapshotFor(revision)
	require.NoError(t, snapshot.err)
	require.Equal(t, []inventoryInstance{{name: valid.Name, uid: valid.UID}}, admittedInstances(snapshot.admitted))
}

func TestCapacityAdmissionChargesOnlyMaterializableGroups(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	partial := admissionTestInventory("partial", "partial-uid", profile.Name, 60_000, 1)
	partial.Spec.RackGroups = append(partial.Spec.RackGroups, mokkav1alpha1.RackGroup{
		ID: "unresolved", Count: 40_000,
		ProfileRef: mokkav1alpha1.ProfileReference{Name: "missing"},
	})
	follower := admissionTestInventory("follower", "follower-uid", profile.Name, 40_000, 2)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{follower, partial},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()

	partialCapacity, materializes, err := materializedInventoryCapacity(source, partial)
	require.NoError(t, err)
	require.True(t, materializes)
	require.Equal(t, DeclaredCapacity{Racks: 60_000, Nodes: 60_000, GPUs: 60_000}, partialCapacity)
	require.True(t, capacityAdmissionDecision(t, admission, revision, partial))
	require.True(t, capacityAdmissionDecision(t, admission, revision, follower))
}

func TestCapacityAdmissionChargesPreservedUnresolvedGroups(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1_000, 1)
	partial := admissionTestInventory("partial", "partial-uid", profile.Name, 60, 1)
	partial.Spec.RackGroups = append(partial.Spec.RackGroups, mokkav1alpha1.RackGroup{
		ID: "unresolved", Count: 40,
		ProfileRef: mokkav1alpha1.ProfileReference{Name: "missing"},
	})
	follower := admissionTestInventory("follower", "follower-uid", profile.Name, 40, 2)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{partial, follower},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	for index := range 60 {
		source.racks = append(source.racks, admissionRack(partial, int32(index), 1_000))
	}
	for index := range 40 {
		rack := admissionRack(partial, int32(60+index), 1_000)
		rack.Spec.Identity.RackGroup = "unresolved"
		source.racks = append(source.racks, rack)
	}

	admission := NewCapacityAdmission(source)
	capacity, materializes, err := materializedInventoryCapacity(source, follower)
	require.NoError(t, err)
	require.True(t, materializes)
	decision, err := admission.decision(admission.currentRevision(), follower, capacity)
	require.NoError(t, err)
	require.Equal(t, capacityAdmissionRejected, decision,
		"an unresolved group's last-good racks remain part of its admitted topology")
}

func TestCapacityAdmissionCoalescesConcurrentWorkers(t *testing.T) {
	const workerCount = 64
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	inventory := admissionTestInventory("inventory", "inventory-uid", profile.Name, 100_000, 1)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{inventory},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}
	admission := NewCapacityAdmission(source)
	revision := admission.currentRevision()
	capacity, materializes, err := materializedInventoryCapacity(source, inventory)
	require.NoError(t, err)
	require.True(t, materializes)

	start := make(chan struct{})
	errors := make([]error, workerCount)
	decisions := make([]bool, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for index := range workerCount {
		go func() {
			defer workers.Done()
			<-start
			decisions[index], errors[index] = admission.admits(revision, inventory, capacity)
		}()
	}
	close(start)
	workers.Wait()

	for index := range workerCount {
		require.NoError(t, errors[index])
		require.True(t, decisions[index])
	}
	require.EqualValues(t, 1, admission.computations.Load())
}

func TestCapacityAdmission100KBoundUsesLinearBoundedState(t *testing.T) {
	const declarationCount = MaxInventoryNodes + 1
	candidates := make([]admissionInventory, declarationCount)
	for index := range candidates {
		candidates[index] = admissionInventory{
			instance:       inventoryInstance{name: fmt.Sprintf("inventory-%06d", index)},
			targetCapacity: DeclaredCapacity{Racks: 1, Nodes: 1, GPUs: 1},
		}
	}

	admitted := admitCapacityCandidates(DeclaredCapacity{}, candidates)

	require.Len(t, admitted, int(MaxInventoryNodes))
	require.Equal(t, "inventory-099999", admitted[len(admitted)-1].instance.name)
}

func TestReconcileRejectsAggregateCrossInventoryCapacityBeforeAllocationOrWrites(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 60_000, 1)
	blocked := admissionTestInventory("blocked", "blocked-uid", profile.Name, 60_000, 2)
	blocked.Finalizers = []string{InventoryFinalizer}
	h := newHarness(t, []runtime.Object{profile, first, blocked}, nil)
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

	result, err := reconciler.Reconcile(ctx, blocked.Name)

	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.Equal(t, ReasonCapacityExceeded, result.ValidationReason)
	require.Contains(t, result.ValidationError, "admitted whole by oldest-first fit")
	require.Zero(t, allocationCalls)
	require.Zero(t, result.Work)
	require.Empty(t, h.mokka.Actions())
}

func TestAllocationInputExcludesAggregateRejectedInventories(t *testing.T) {
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	first := admissionTestInventory("first", "first-uid", profile.Name, 60_000, 1)
	blocked := admissionTestInventory("blocked", "blocked-uid", profile.Name, 60_000, 2)
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{blocked, first},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
	}

	input, err := allocationInput(source)

	require.NoError(t, err)
	require.Len(t, input.Groups, 1)
	require.Equal(t, first.Name, input.Groups[0].Key.InventoryName)
}

func TestReconcileStopsStaleMaterializationAfterAdmissionChanges(t *testing.T) {
	ctx := context.Background()
	profile := testProfile("profile", "profile-uid", 1, 1, 1)
	inventory := admissionTestInventory("inventory", "inventory-uid", profile.Name, 2, 1)
	inventory.Finalizers = []string{InventoryFinalizer}
	h := newHarness(t, []runtime.Object{profile, inventory}, nil)
	allocation := NewAllocationCache(h.cache)
	reconciler := NewReconcilerWithAllocationCache(
		h.cache,
		h.mokka.MokkaV1alpha1().SGPUInventories(),
		h.mokka.MokkaV1alpha1().SGPURacks(),
		CleanupGateFunc(func(CleanupNeeded) bool { return false }),
		allocation,
	)
	created := 0
	h.mokka.Fake.PrependReactor("create", "sgpuracks", func(clienttesting.Action) (bool, runtime.Object, error) {
		created++
		if created == 1 {
			allocation.InvalidateCapacity()
		}
		return false, nil, nil
	})

	result, err := reconciler.Reconcile(ctx, inventory.Name)

	require.ErrorIs(t, err, errAllocationInputChanged)
	require.Equal(t, 1, created)
	require.EqualValues(t, 1, result.Work.RacksReconciled)
}

func capacityAdmissionDecision(
	t *testing.T,
	admission *CapacityAdmission,
	revision capacityRevision,
	inventory *mokkav1alpha1.SGPUInventory,
) bool {
	t.Helper()
	capacity, materializes, err := materializedInventoryCapacity(admission.cache, inventory)
	require.NoError(t, err)
	require.True(t, materializes)
	admitted, err := admission.admits(revision, inventory, capacity)
	require.NoError(t, err)
	return admitted
}

func admissionTestInventory(
	name string,
	uid types.UID,
	profileName string,
	count int32,
	created int64,
) *mokkav1alpha1.SGPUInventory {
	inventory := testInventory(name, uid, profileName, count)
	inventory.CreationTimestamp = metav1.NewTime(time.Unix(created, 0))
	inventory.ResourceVersion = "1"
	return inventory
}

func expandAdmissionRackGroups(inventory *mokkav1alpha1.SGPUInventory, groupCount int) {
	template := inventory.Spec.RackGroups[0]
	inventory.Spec.RackGroups = make([]mokkav1alpha1.RackGroup, groupCount)
	for index := range inventory.Spec.RackGroups {
		group := template
		group.ID = fmt.Sprintf("group-%02d", index)
		inventory.Spec.RackGroups[index] = group
	}
}

func admittedInstances(admitted []admissionInventory) []inventoryInstance {
	instances := make([]inventoryInstance, len(admitted))
	for index := range admitted {
		instances[index] = admitted[index].instance
	}
	return instances
}

func admissionRack(
	inventory *mokkav1alpha1.SGPUInventory,
	index int32,
	nodes int,
) *mokkav1alpha1.SGPURack {
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-rack-%d", inventory.Name, index),
			UID:  types.UID(fmt.Sprintf("%s-rack-uid-%d", inventory.Name, index)),
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
				inventory,
				mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"),
			)},
		},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{
				Name: inventory.Name,
				UID:  inventory.UID,
			},
			Identity: mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: index},
			Nodes:    make([]mokkav1alpha1.SGPURackNode, nodes),
		},
	}
}
