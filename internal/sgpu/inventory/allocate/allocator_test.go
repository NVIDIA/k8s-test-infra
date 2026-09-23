// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package allocate

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestAllocatorRetainsBindingsAndFillsOnlyFreeSlots(t *testing.T) {
	group := RackGroup{Key: groupKey("inventory-a", "compute"), Racks: 2, NodesPerRack: 2}
	existing := binding(group.Key, 1, 0, "node-b", "uid-b")
	input := Snapshot{
		Groups: []RackGroup{group},
		Nodes: []KubernetesNode{
			node("node-d", "uid-d", 4, eligibleLabels()),
			node("node-b", "uid-b", 2, eligibleLabels()),
			node("node-c", "uid-c", 3, eligibleLabels()),
			node("node-a", "uid-a", 1, eligibleLabels()),
			node("node-e", "uid-e", 5, eligibleLabels()),
		},
		Bindings: []Binding{existing},
	}

	plan, err := Allocate(input)
	require.NoError(t, err)
	require.Equal(t, []Binding{existing}, plan.Retained)
	require.Equal(t, []Binding{
		binding(group.Key, 0, 0, "node-a", "uid-a"),
		binding(group.Key, 0, 1, "node-c", "uid-c"),
		binding(group.Key, 1, 1, "node-d", "uid-d"),
	}, plan.Assigned)
	require.Equal(t, []KubernetesNode{node("node-e", "uid-e", 5, eligibleLabels())}, plan.Pending)
	require.Empty(t, plan.Released)
	require.Empty(t, plan.Conflicts)
	require.LessOrEqual(t, plan.Stats.NodeVisits, int64(4))

	// A newly observed older Node cannot move an existing durable binding.
	restart := input
	restart.Bindings = slices.Concat(plan.Retained, plan.Assigned)
	restart.Nodes = append(restart.Nodes, node("node-00", "uid-00", 0, eligibleLabels()))
	restarted, err := Allocate(restart)
	require.NoError(t, err)
	require.ElementsMatch(t, restart.Bindings, restarted.Retained)
	require.Empty(t, restarted.Assigned)
	require.Equal(t, []KubernetesNode{
		node("node-00", "uid-00", 0, eligibleLabels()),
		node("node-e", "uid-e", 5, eligibleLabels()),
	}, restarted.Pending)
}

func TestAllocatorSortsPendingNodesByCreationNameAndUID(t *testing.T) {
	group := RackGroup{Key: groupKey("inventory-a", "compute"), Racks: 1, NodesPerRack: 3}
	created := time.Unix(10, 0).UTC()
	input := Snapshot{
		Groups: []RackGroup{group},
		Nodes: []KubernetesNode{
			{Name: "same", UID: "uid-z", CreationTimestamp: created, Labels: eligibleLabels()},
			{Name: "z-last", UID: "uid-b", CreationTimestamp: created, Labels: eligibleLabels()},
			{Name: "same", UID: "uid-a", CreationTimestamp: created, Labels: eligibleLabels()},
		},
	}

	plan, err := Allocate(input)
	require.NoError(t, err)
	require.Equal(t, []Binding{
		binding(group.Key, 0, 0, "same", "uid-a"),
		binding(group.Key, 0, 1, "same", "uid-z"),
		binding(group.Key, 0, 2, "z-last", "uid-b"),
	}, plan.Assigned)
}

func TestAllocatorReportsSelectorOverlapUnlessAValidBindingExists(t *testing.T) {
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "shared"}}
	groupA := RackGroup{Key: groupKey("inventory-a", "compute"), Selector: selector, Racks: 1, NodesPerRack: 2}
	groupB := RackGroup{Key: groupKey("inventory-b", "compute"), Selector: selector, Racks: 1, NodesPerRack: 2}
	bound := binding(groupA.Key, 0, 1, "bound", "bound-uid")
	input := Snapshot{
		Groups: []RackGroup{groupB, groupA},
		Nodes: []KubernetesNode{
			node("unbound", "unbound-uid", 1, map[string]string{EligibleNodeLabel: "true", "pool": "shared"}),
			node("bound", "bound-uid", 2, map[string]string{EligibleNodeLabel: "true", "pool": "shared"}),
		},
		Bindings: []Binding{bound},
	}

	plan, err := Allocate(input)
	require.NoError(t, err)
	require.Equal(t, []Binding{bound}, plan.Retained)
	require.Empty(t, plan.Assigned)
	require.Empty(t, plan.Pending)
	require.Equal(t, []Conflict{{
		Kind:       ConflictSelectorOverlap,
		Node:       input.Nodes[0],
		Candidates: []RackGroupKey{groupA.Key, groupB.Key},
	}}, plan.Conflicts)
}

func TestAllocatorPreservesDuplicateBindingsAsADataConflict(t *testing.T) {
	groupA := RackGroup{Key: groupKey("inventory-a", "compute"), Racks: 1, NodesPerRack: 2}
	groupB := RackGroup{Key: groupKey("inventory-b", "compute"), Racks: 1, NodesPerRack: 2}
	n := node("node-a", "node-uid", 1, eligibleLabels())
	first := binding(groupA.Key, 0, 0, n.Name, n.UID)
	second := binding(groupB.Key, 0, 1, n.Name, n.UID)

	plan, err := Allocate(Snapshot{
		Groups:   []RackGroup{groupA, groupB},
		Nodes:    []KubernetesNode{n},
		Bindings: []Binding{second, first},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []Binding{first, second}, plan.Retained)
	require.Empty(t, plan.Released)
	require.Empty(t, plan.Assigned)
	require.Equal(t, []Conflict{{
		Kind:     ConflictDuplicateBinding,
		Node:     n,
		Bindings: []Binding{first, second},
	}}, plan.Conflicts)
}

func TestAllocatorPlansBindingReleases(t *testing.T) {
	matching := &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "matching"}}
	group := RackGroup{Key: groupKey("inventory-a", "compute"), Selector: matching, Racks: 1, NodesPerRack: 3}
	removedGroup := groupKey("inventory-a", "removed")
	inputs := []Binding{
		binding(group.Key, 0, 0, "gone", "gone-uid"),
		binding(group.Key, 0, 1, "ineligible", "ineligible-uid"),
		binding(group.Key, 0, 3, "shrunk-slot", "shrunk-slot-uid"),
		binding(group.Key, 1, 0, "shrunk-rack", "shrunk-rack-uid"),
		binding(group.Key, 0, 2, "mismatch", "mismatch-uid"),
		binding(removedGroup, 0, 0, "removed", "removed-uid"),
	}
	nodes := []KubernetesNode{
		node("ineligible", "ineligible-uid", 1, map[string]string{"pool": "matching"}),
		node("shrunk-slot", "shrunk-slot-uid", 2, map[string]string{EligibleNodeLabel: "true", "pool": "matching"}),
		node("shrunk-rack", "shrunk-rack-uid", 3, map[string]string{EligibleNodeLabel: "true", "pool": "matching"}),
		node("mismatch", "mismatch-uid", 4, map[string]string{EligibleNodeLabel: "true", "pool": "other"}),
		node("removed", "removed-uid", 5, map[string]string{EligibleNodeLabel: "true", "pool": "matching"}),
	}

	plan, err := Allocate(Snapshot{Groups: []RackGroup{group}, Nodes: nodes, Bindings: inputs})
	require.NoError(t, err)
	require.ElementsMatch(t, []Release{
		{Binding: inputs[0], Reason: ReleaseNodeGone},
		{Binding: inputs[1], Reason: ReleaseNodeIneligible},
		{Binding: inputs[2], Reason: ReleaseCapacityShrink},
		{Binding: inputs[3], Reason: ReleaseCapacityShrink},
		{Binding: inputs[4], Reason: ReleaseSelectorMismatch},
		{Binding: inputs[5], Reason: ReleaseGroupRemoved},
	}, plan.Released)
	require.Empty(t, plan.Retained)
	require.Empty(t, plan.Assigned)

	// Existing bindings occupy coordinates until their exact cleanup is applied.
	require.Equal(t, []KubernetesNode{nodes[1], nodes[2], nodes[4]}, plan.Pending)
}

func TestAllocatorExcludesTerminatingNodes(t *testing.T) {
	group := RackGroup{Key: groupKey("inventory-a", "compute"), Racks: 1, NodesPerRack: 2}
	bound := node("bound", "bound-uid", 1, eligibleLabels())
	bound.Terminating = true
	unbound := node("unbound", "unbound-uid", 2, eligibleLabels())
	unbound.Terminating = true
	binding := binding(group.Key, 0, 0, bound.Name, bound.UID)

	plan, err := Allocate(Snapshot{
		Groups:   []RackGroup{group},
		Nodes:    []KubernetesNode{bound, unbound},
		Bindings: []Binding{binding},
	})

	require.NoError(t, err)
	require.Empty(t, plan.Retained)
	require.Equal(t, []Release{{Binding: binding, Reason: ReleaseNodeIneligible}}, plan.Released)
	require.Empty(t, plan.Assigned)
	require.Empty(t, plan.Pending)
}

func TestAllocatorHandlesSameNameNewUIDAsAReplacement(t *testing.T) {
	group := RackGroup{Key: groupKey("inventory-a", "compute"), Racks: 1, NodesPerRack: 1}
	oldBinding := binding(group.Key, 0, 0, "same-name", "old-uid")
	replacement := node("same-name", "new-uid", 2, eligibleLabels())

	first, err := Allocate(Snapshot{
		Groups:   []RackGroup{group},
		Nodes:    []KubernetesNode{replacement},
		Bindings: []Binding{oldBinding},
	})
	require.NoError(t, err)
	require.Equal(t, []Release{{Binding: oldBinding, Reason: ReleaseNodeGone}}, first.Released)
	require.Empty(t, first.Assigned)
	require.Equal(t, []KubernetesNode{replacement}, first.Pending)

	second, err := Allocate(Snapshot{Groups: []RackGroup{group}, Nodes: []KubernetesNode{replacement}})
	require.NoError(t, err)
	require.Equal(t, []Binding{binding(group.Key, 0, 0, "same-name", "new-uid")}, second.Assigned)
}

func TestAllocatorDoesNotCompactAcrossShrinkAndGrowth(t *testing.T) {
	key := groupKey("inventory-a", "compute")
	low := binding(key, 0, 1, "low", "low-uid")
	high := binding(key, 2, 0, "high", "high-uid")
	nodes := []KubernetesNode{
		node("low", "low-uid", 1, eligibleLabels()),
		node("high", "high-uid", 2, eligibleLabels()),
	}

	shrunk, err := Allocate(Snapshot{
		Groups:   []RackGroup{{Key: key, Racks: 2, NodesPerRack: 2}},
		Nodes:    nodes,
		Bindings: []Binding{high, low},
	})
	require.NoError(t, err)
	require.Equal(t, []Binding{low}, shrunk.Retained)
	require.Equal(t, []Release{{Binding: high, Reason: ReleaseCapacityShrink}}, shrunk.Released)
	require.Empty(t, shrunk.Assigned)

	grown, err := Allocate(Snapshot{
		Groups:   []RackGroup{{Key: key, Racks: 4, NodesPerRack: 2}},
		Nodes:    nodes,
		Bindings: []Binding{low},
	})
	require.NoError(t, err)
	require.Equal(t, []Binding{low}, grown.Retained)
	require.Equal(t, []Binding{binding(key, 0, 0, "high", "high-uid")}, grown.Assigned)
}

func TestAllocatorRejectsMalformedSnapshot(t *testing.T) {
	key := groupKey("inventory-a", "compute")
	tests := []struct {
		name  string
		input Snapshot
		err   string
	}{
		{
			name:  "negative capacity",
			input: Snapshot{Groups: []RackGroup{{Key: key, Racks: -1, NodesPerRack: 1}}},
			err:   "capacity",
		},
		{
			name: "duplicate live UID",
			input: Snapshot{Groups: []RackGroup{{Key: key, Racks: 1, NodesPerRack: 1}}, Nodes: []KubernetesNode{
				node("one", "same-uid", 1, eligibleLabels()),
				node("two", "same-uid", 2, eligibleLabels()),
			}},
			err: "duplicate node UID",
		},
		{
			name: "two Nodes in one coordinate",
			input: Snapshot{
				Groups: []RackGroup{{Key: key, Racks: 1, NodesPerRack: 1}},
				Nodes: []KubernetesNode{
					node("one", "one-uid", 1, eligibleLabels()),
					node("two", "two-uid", 2, eligibleLabels()),
				},
				Bindings: []Binding{
					binding(key, 0, 0, "one", "one-uid"),
					binding(key, 0, 0, "two", "two-uid"),
				},
			},
			err: "duplicate logical Node binding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Allocate(tt.input)
			require.ErrorContains(t, err, tt.err)
		})
	}
}

func BenchmarkAllocator100kNodes(b *testing.B) {
	const nodeCount = 100_000
	key := groupKey("inventory-a", "compute")
	nodes := make([]KubernetesNode, nodeCount)
	for i := range nodes {
		nodes[i] = node(
			fmt.Sprintf("node-%06d", nodeCount-i),
			types.UID(fmt.Sprintf("uid-%06d", i)),
			int64(i),
			eligibleLabels(),
		)
	}
	input := Snapshot{
		Groups: []RackGroup{{Key: key, Racks: 1000, NodesPerRack: 100}},
		Nodes:  nodes,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		plan, err := Allocate(input)
		require.NoError(b, err)
		require.Len(b, plan.Assigned, nodeCount)
		require.LessOrEqual(b, plan.Stats.SelectorEvaluations, int64(nodeCount))
		require.LessOrEqual(b, plan.Stats.NodeVisits, int64(nodeCount))
		require.LessOrEqual(b, plan.Stats.BindingLookups, int64(nodeCount))
	}
}

func TestRackGroupKeyCompareOrdersByInventoryThenGroup(t *testing.T) {
	t.Parallel()

	key := RackGroupKey{InventoryName: "b", InventoryUID: "b-uid", RackGroup: "b"}

	require.Zero(t, key.Compare(key))
	require.Equal(t, 1, key.Compare(RackGroupKey{InventoryName: "a", InventoryUID: "z-uid", RackGroup: "z"}),
		"inventory name decides first")
	require.Equal(t, -1, key.Compare(RackGroupKey{InventoryName: "b", InventoryUID: "c-uid", RackGroup: "a"}),
		"then inventory UID")
	require.Equal(t, 1, key.Compare(RackGroupKey{InventoryName: "b", InventoryUID: "b-uid", RackGroup: "a"}),
		"then rack group")
}

func TestBindingCompareOrdersByCoordinateThenNodeUID(t *testing.T) {
	t.Parallel()

	key := groupKey("inventory-b", "compute")
	base := binding(key, 1, 1, "node", "uid-b")

	require.Zero(t, base.Compare(base))
	require.Equal(t, 1, base.Compare(binding(groupKey("inventory-a", "compute"), 9, 9, "node", "uid-z")),
		"rack group decides first")
	require.Equal(t, -1, base.Compare(binding(key, 2, 0, "node", "uid-a")), "then rack index")
	require.Equal(t, 1, base.Compare(binding(key, 1, 0, "node", "uid-z")), "then node index")
	require.Equal(t, -1, base.Compare(binding(key, 1, 1, "node", "uid-c")), "then Node UID")
}

func node(name string, uid types.UID, seconds int64, labels map[string]string) KubernetesNode {
	return KubernetesNode{
		Name:              name,
		UID:               uid,
		CreationTimestamp: time.Unix(seconds, 0).UTC(),
		Labels:            labels,
	}
}

func eligibleLabels() map[string]string {
	return map[string]string{EligibleNodeLabel: "true"}
}

func binding(key RackGroupKey, rack, slot int32, name string, uid types.UID) Binding {
	return Binding{
		Coordinate: Coordinate{Group: key, RackIndex: rack, NodeIndex: slot},
		Node:       NodeReference{Name: name, UID: uid},
	}
}
