// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
)

func TestAllocationCacheCoalescesConcurrentGroupViews(t *testing.T) {
	const groupCount = 64
	source, inventory, keys := allocationScaleSource(groupCount, groupCount)
	planner := NewAllocationCache(source)

	started := make(chan struct{})
	release := make(chan struct{})
	var allocateCalls atomic.Int64
	planner.allocate = func(input allocate.Input) (allocate.Plan, error) {
		if allocateCalls.Add(1) == 1 {
			close(started)
		}
		<-release
		return allocate.Allocate(input)
	}

	start := make(chan struct{})
	views := make([]allocate.Plan, groupCount)
	errors := make([]error, groupCount)
	var ready sync.WaitGroup
	var workers sync.WaitGroup
	ready.Add(groupCount)
	workers.Add(groupCount)
	for index := range groupCount {
		go func() {
			defer workers.Done()
			ready.Done()
			<-start
			views[index], errors[index] = planner.plan(&keys[index], instanceForGroup(keys[index]))
		}()
	}
	ready.Wait()
	close(start)
	<-started
	close(release)
	workers.Wait()

	for index, view := range views {
		require.NoError(t, errors[index])
		require.Len(t, view.Assigned, 1)
		require.Equal(t, keys[index], view.Assigned[0].Coordinate.Group)
		require.Len(t, view.Bindings, 1, "a group view must not retain another group's global slice")
	}
	require.EqualValues(t, 1, allocateCalls.Load())
	require.EqualValues(t, 1, planner.Stats().Computations)

	full, err := planner.plan(nil, inventoryInstance{name: inventory.Name, uid: inventory.UID})
	require.NoError(t, err)
	require.Len(t, full.Assigned, groupCount)
	for index := range views {
		require.Same(t, &full.Assigned[index], &views[index].Assigned[0],
			"group views must share their inventory partition instead of retaining copies")
	}
	require.EqualValues(t, 1, planner.Stats().Computations, "stable inventory and group views must share one computation")

	planner.mu.Lock()
	defer planner.mu.Unlock()
	require.Len(t, planner.snapshot.groups, groupCount)
	partitioned := 0
	for _, group := range planner.snapshot.groups {
		require.Empty(t, group.Bindings, "derived binding views must not be retained twice")
		partitioned += len(group.Assigned) + len(group.Retained)
	}
	require.Equal(t, groupCount, partitioned, "the snapshot retains each global binding in exactly one group partition")
}

func TestAllocationCacheInvalidatesNodeIdentitySpecsAndRackBindings(t *testing.T) {
	source, inventory, keys := allocationScaleSource(1, 1)
	key := keys[0]
	oldNode := source.nodes[0]
	source.racks = []*mokkav1alpha1.SGPURack{allocationRack(inventory, key, "rack-uid-1", &mokkav1alpha1.SGPUNodeReference{
		Name: oldNode.Name, UID: oldNode.UID,
	})}
	planner := NewAllocationCache(source)

	stable, err := planner.plan(&key, instanceForGroup(key))
	require.NoError(t, err)
	require.Len(t, stable.Retained, 1)
	_, err = planner.plan(&key, instanceForGroup(key))
	require.NoError(t, err)
	require.EqualValues(t, 1, planner.Stats().Computations)

	source.mu.Lock()
	replacement := oldNode
	replacement.UID = "replacement-uid"
	source.nodes = []allocate.Node{replacement}
	source.nodeGeneration++
	source.mu.Unlock()
	recreated, err := planner.plan(&key, instanceForGroup(key))
	require.NoError(t, err)
	require.Len(t, recreated.Released, 1)
	require.Equal(t, oldNode.UID, recreated.Released[0].Binding.Node.UID)
	require.Equal(t, allocate.ReleaseNodeGone, recreated.Released[0].Reason)
	require.Equal(t, []allocate.Node{replacement}, recreated.Pending)
	require.EqualValues(t, 2, planner.Stats().Computations)

	source.mu.Lock()
	cleared := source.racks[0].DeepCopy()
	cleared.UID = "rack-uid-2"
	cleared.Spec.Nodes[0].NodeRef = nil
	source.racks = []*mokkav1alpha1.SGPURack{cleared}
	source.mu.Unlock()
	planner.Invalidate()
	clearedPlan, err := planner.plan(&key, instanceForGroup(key))
	require.NoError(t, err)
	require.Len(t, clearedPlan.Assigned, 1)
	require.Equal(t, replacement.UID, clearedPlan.Assigned[0].Node.UID)
	require.EqualValues(t, 3, planner.Stats().Computations)

	source.mu.Lock()
	updatedInventory := source.inventories[0].DeepCopy()
	updatedInventory.Spec.RackGroups[0].Placement.NodeSelector.MatchLabels["pool"] = "other"
	source.inventories = []*mokkav1alpha1.SGPUInventory{updatedInventory}
	source.mu.Unlock()
	planner.Invalidate()
	specPlan, err := planner.plan(&key, instanceForGroup(key))
	require.NoError(t, err)
	require.Empty(t, specPlan.Assigned)
	require.EqualValues(t, 4, planner.Stats().Computations)
}

func TestBindingDesiredRevisionFencesEveryAllocationInput(t *testing.T) {
	source, inventory, keys := allocationScaleSource(1, 1)
	key := keys[0]
	node := source.nodes[0]
	source.racks = []*mokkav1alpha1.SGPURack{allocationRack(inventory, key, "rack-uid", &mokkav1alpha1.SGPUNodeReference{
		Name: node.Name, UID: node.UID,
	})}
	planner := NewAllocationCache(source)
	binding := allocate.Binding{
		Coordinate: allocate.Coordinate{Group: key},
		Node:       allocate.NodeReference{Name: node.Name, UID: node.UID},
	}

	desired, revision, err := planner.BindingDesiredRevision(binding)
	require.NoError(t, err)
	require.True(t, desired)
	require.True(t, planner.RevisionCurrent(revision))

	planner.InvalidateAllocation()
	require.False(t, planner.RevisionCurrent(revision), "topology changes must invalidate the cleanup decision")
	_, revision, err = planner.BindingDesiredRevision(binding)
	require.NoError(t, err)
	planner.InvalidateCapacity()
	require.False(t, planner.RevisionCurrent(revision), "capacity admission changes must invalidate the cleanup decision")
	_, revision, err = planner.BindingDesiredRevision(binding)
	require.NoError(t, err)

	source.mu.Lock()
	source.nodeGeneration++
	source.mu.Unlock()
	require.False(t, planner.RevisionCurrent(revision), "Node input changes must invalidate the cleanup decision")
}

func TestAllocationCacheRejectsGenerationChangedDuringComputation(t *testing.T) {
	source, _, keys := allocationScaleSource(1, 1)
	key := keys[0]
	planner := NewAllocationCache(source)
	revision := planner.revision()
	started := make(chan struct{})
	release := make(chan struct{})
	planner.allocate = func(input allocate.Input) (allocate.Plan, error) {
		close(started)
		<-release
		return allocate.Allocate(input)
	}

	result := make(chan error, 1)
	go func() {
		_, err := planner.planRevision(revision, &key, instanceForGroup(key))
		result <- err
	}()
	<-started
	source.mu.Lock()
	replacement := source.nodes[0]
	replacement.UID = "replacement-uid"
	source.nodes = []allocate.Node{replacement}
	source.nodeGeneration++
	source.mu.Unlock()
	close(release)
	require.ErrorIs(t, <-result, errAllocationInputChanged)

	planner.allocate = allocate.Allocate
	view, err := planner.plan(&key, instanceForGroup(key))
	require.NoError(t, err)
	require.Len(t, view.Assigned, 1)
	require.Equal(t, replacement.UID, view.Assigned[0].Node.UID)
	require.EqualValues(t, 2, planner.Stats().Computations)
}

func TestAllocationInputPreservedBindingsOccupyNodesUntilInventoryRecovers(t *testing.T) {
	preservedProfile := testProfile("preserved-profile", "preserved-profile-uid", 1, 1, 1)
	contenderProfile := testProfile("contender-profile", "contender-profile-uid", 1, 1, 1)
	preserved := testInventory("preserved", "preserved-uid", preservedProfile.Name, 1)
	contender := testInventory("contender", "contender-uid", contenderProfile.Name, 1)
	node := allocate.Node{
		Name: "node", UID: "node-uid",
		Labels: map[string]string{allocate.EligibleNodeLabel: "true", "pool": "gpu"},
	}
	preservedKey := allocate.GroupKey{
		InventoryName: preserved.Name, InventoryUID: preserved.UID, RackGroup: "group",
	}
	preservedBinding := allocate.Binding{
		Coordinate: allocate.Coordinate{Group: preservedKey},
		Node:       allocate.NodeReference{Name: node.Name, UID: node.UID},
	}
	source := &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{preserved, contender},
		profiles: map[string]*mokkav1alpha1.SGPURackProfile{
			contenderProfile.Name: contenderProfile,
		},
		racks: []*mokkav1alpha1.SGPURack{
			allocationRack(preserved, preservedKey, "rack-uid", &mokkav1alpha1.SGPUNodeReference{
				Name: node.Name, UID: node.UID,
			}),
		},
		nodes: []allocate.Node{node},
	}

	unresolvedInput, err := allocationInput(source)
	require.NoError(t, err)
	require.Len(t, unresolvedInput.Groups, 1)
	require.Equal(t, contender.Name, unresolvedInput.Groups[0].Key.InventoryName)
	unresolvedPlan, err := allocate.Allocate(unresolvedInput)
	require.NoError(t, err)
	require.Empty(t, unresolvedPlan.Assigned, "a preserved binding must keep its Node unavailable")
	require.Equal(t, []allocate.Binding{preservedBinding}, unresolvedInput.Bindings)
	require.Equal(t, []allocate.Release{{
		Binding: preservedBinding, Reason: allocate.ReleaseGroupRemoved,
	}}, unresolvedPlan.Released)
	require.Equal(t, []allocate.Node{node}, unresolvedPlan.Pending)

	source.mu.Lock()
	source.profiles[preservedProfile.Name] = preservedProfile
	source.mu.Unlock()
	recoveredInput, err := allocationInput(source)
	require.NoError(t, err)
	require.Len(t, recoveredInput.Groups, 2)
	recoveredPlan, err := allocate.Allocate(recoveredInput)
	require.NoError(t, err)
	require.Equal(t, []allocate.Binding{preservedBinding}, recoveredPlan.Retained)
	require.Equal(t, []allocate.Binding{preservedBinding}, recoveredPlan.Bindings)
	require.Empty(t, recoveredPlan.Assigned)
	require.Empty(t, recoveredPlan.Released)
	require.Empty(t, recoveredPlan.Conflicts)
}

func BenchmarkAllocationCache100KNodes64Groups(b *testing.B) {
	const (
		nodeCount  = 100_000
		groupCount = 64
	)
	source, inventory, keys := allocationScaleSource(nodeCount, groupCount)
	planner := NewAllocationCache(source)
	input, err := allocationInput(source)
	require.NoError(b, err)
	require.Len(b, input.Nodes, nodeCount)
	require.Len(b, input.Groups, groupCount, "the scale inventory must pass capacity validation")
	expectedBindings := nodeCount - nodeCount%groupCount
	declaredSlots := int64(0)
	for _, group := range input.Groups {
		declaredSlots += int64(group.Racks) * int64(group.NodesPerRack)
	}
	require.EqualValues(b, expectedBindings, declaredSlots)
	require.LessOrEqual(b, declaredSlots, MaxInventoryNodes)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		planner.Invalidate()
		for index := range groupCount {
			view, err := planner.plan(&keys[index], instanceForGroup(keys[index]))
			if err != nil {
				b.Fatal(err)
			}
			if len(view.Bindings) != expectedBindings/groupCount {
				b.Fatalf(
					"group %d has %d bindings, want %d",
					index,
					len(view.Bindings),
					expectedBindings/groupCount,
				)
			}
		}
		view, err := planner.plan(nil, inventoryInstance{name: inventory.Name, uid: inventory.UID})
		if err != nil {
			b.Fatal(err)
		}
		if len(view.Bindings) != expectedBindings {
			b.Fatalf("got %d bindings, want %d", len(view.Bindings), expectedBindings)
		}
		if len(view.Pending) != nodeCount-expectedBindings {
			b.Fatalf("got %d pending Nodes, want %d", len(view.Pending), nodeCount-expectedBindings)
		}
	}
	b.StopTimer()
	require.EqualValues(b, b.N, planner.Stats().Computations)
	b.ReportMetric(float64(planner.Stats().Computations)/float64(b.N), "global-plans/op")
}

type mutableAllocationSource struct {
	mu             sync.RWMutex
	inventories    []*mokkav1alpha1.SGPUInventory
	profiles       map[string]*mokkav1alpha1.SGPURackProfile
	racks          []*mokkav1alpha1.SGPURack
	nodes          []allocate.Node
	nodeGeneration uint64
}

func (s *mutableAllocationSource) Inventory(name string) (*mokkav1alpha1.SGPUInventory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inventory := range s.inventories {
		if inventory.Name == name {
			return inventory, nil
		}
	}
	return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuinventories"), name)
}

func (s *mutableAllocationSource) Inventories() ([]*mokkav1alpha1.SGPUInventory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*mokkav1alpha1.SGPUInventory(nil), s.inventories...), nil
}

func (s *mutableAllocationSource) Profile(name string) (*mokkav1alpha1.SGPURackProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile := s.profiles[name]
	if profile == nil {
		return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpurackprofiles"), name)
	}
	return profile, nil
}

func (s *mutableAllocationSource) Rack(name string) (*mokkav1alpha1.SGPURack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rack := range s.racks {
		if rack.Name == name {
			return rack, nil
		}
	}
	return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), name)
}

func (s *mutableAllocationSource) Racks() ([]*mokkav1alpha1.SGPURack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*mokkav1alpha1.SGPURack(nil), s.racks...), nil
}

func (s *mutableAllocationSource) RacksByInventoryUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var racks []*mokkav1alpha1.SGPURack
	for _, rack := range s.racks {
		if rack.Spec.InventoryRef.UID == uid {
			racks = append(racks, rack)
		}
	}
	return racks, nil
}

func (s *mutableAllocationSource) RacksByInventoryGroup(
	uid types.UID,
	group string,
) ([]*mokkav1alpha1.SGPURack, error) {
	ranks, err := s.RacksByInventoryUID(uid)
	if err != nil {
		return nil, err
	}
	return slicesDeleteRackGroup(ranks, group), nil
}

func (s *mutableAllocationSource) AllocationNodeGeneration() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodeGeneration
}

func (s *mutableAllocationSource) AllocationNodes() ([]allocate.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]allocate.Node(nil), s.nodes...), nil
}

func allocationScaleSource(
	nodeCount int,
	groupCount int,
) (*mutableAllocationSource, *mokkav1alpha1.SGPUInventory, []allocate.GroupKey) {
	perGroup := nodeCount / groupCount
	rackCount := (perGroup + 1023) / 1024
	nodesPerRack := perGroup / rackCount
	profile := testProfile("profile", "profile-uid", 1, int32(nodesPerRack), 1)
	inventory := testInventory("inventory", "inventory-uid", profile.Name, 1)
	inventory.Finalizers = []string{InventoryFinalizer}
	inventory.Spec.RackGroups = make([]mokkav1alpha1.RackGroup, groupCount)
	keys := make([]allocate.GroupKey, groupCount)
	for index := range groupCount {
		group := fmt.Sprintf("group-%02d", index)
		inventory.Spec.RackGroups[index] = mokkav1alpha1.RackGroup{
			ID: group, Count: int32(rackCount),
			ProfileRef: mokkav1alpha1.ProfileReference{Name: profile.Name},
			Placement: &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": group},
			}},
		}
		keys[index] = allocate.GroupKey{
			InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: group,
		}
	}
	nodes := make([]allocate.Node, nodeCount)
	for index := range nodeCount {
		group := fmt.Sprintf("group-%02d", index%groupCount)
		nodes[index] = allocate.Node{
			Name: fmt.Sprintf("node-%06d", index), UID: types.UID(fmt.Sprintf("node-uid-%06d", index)),
			Labels: map[string]string{allocate.EligibleNodeLabel: "true", "pool": group},
		}
	}
	return &mutableAllocationSource{
		inventories: []*mokkav1alpha1.SGPUInventory{inventory},
		profiles:    map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
		nodes:       nodes,
	}, inventory, keys
}

func allocationRack(
	inventory *mokkav1alpha1.SGPUInventory,
	key allocate.GroupKey,
	uid types.UID,
	ref *mokkav1alpha1.SGPUNodeReference,
) *mokkav1alpha1.SGPURack {
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rack", UID: uid,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
				inventory, mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"),
			)},
		},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: key.RackGroup, RackIndex: 0},
			Nodes:        []mokkav1alpha1.SGPURackNode{{Index: 0, NodeRef: ref}},
		},
	}
}

func slicesDeleteRackGroup(racks []*mokkav1alpha1.SGPURack, group string) []*mokkav1alpha1.SGPURack {
	result := make([]*mokkav1alpha1.SGPURack, 0, len(racks))
	for _, rack := range racks {
		if rack.Spec.Identity.RackGroup == group {
			result = append(result, rack)
		}
	}
	return result
}

var _ Cache = (*mutableAllocationSource)(nil)
