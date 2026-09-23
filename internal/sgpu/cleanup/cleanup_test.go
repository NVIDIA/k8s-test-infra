// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package cleanup

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
)

func TestCleanupNeededMatchesRackRequiresExactBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*mokkav1alpha1.SGPURack)
		want   bool
	}{
		{name: "exact binding", want: true},
		{name: "replacement rack", mutate: func(rack *mokkav1alpha1.SGPURack) { rack.UID = "replacement-rack-uid" }},
		{
			name:   "rack of another inventory",
			mutate: func(rack *mokkav1alpha1.SGPURack) { rack.Spec.InventoryRef.UID = "other-inventory-uid" },
		},
		{name: "another rack coordinate", mutate: func(rack *mokkav1alpha1.SGPURack) { rack.Spec.Identity.RackIndex = 1 }},
		{
			name:   "logical Node rebound",
			mutate: func(rack *mokkav1alpha1.SGPURack) { rack.Spec.Nodes[0].NodeRef.UID = "replacement-node-uid" },
		},
		{name: "logical Node unbound", mutate: func(rack *mokkav1alpha1.SGPURack) { rack.Spec.Nodes[0].NodeRef = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			needed, rack := boundRack()
			if tt.mutate != nil {
				tt.mutate(rack)
			}

			require.Equal(t, tt.want, needed.MatchesRack(rack))
		})
	}
}

func TestCleanupReasonClassifiesCauseAndEffect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason        CleanupReason
		revocable     bool
		freesCapacity bool
	}{
		{reason: CleanupCapacityShrink, revocable: true, freesCapacity: true},
		{reason: CleanupCapacityRejected, revocable: true, freesCapacity: true},
		{reason: CleanupGroupRemoved, revocable: true, freesCapacity: true},
		{reason: CleanupNodeIneligible, revocable: true},
		{reason: CleanupSelectorMismatch, revocable: true},
		{reason: CleanupNodeGone, revocable: true},
		{reason: CleanupRackDeleting, freesCapacity: true},
		{reason: CleanupInventoryDeleting, freesCapacity: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.revocable, tt.reason.Revocable())
			require.Equal(t, tt.freesCapacity, tt.reason.FreesCapacity())
		})
	}
}

func TestReasonForReleaseCoversEveryAllocatorRelease(t *testing.T) {
	t.Parallel()

	require.Equal(t, CleanupGroupRemoved, ReasonForRelease(allocate.ReleaseGroupRemoved))
	require.Equal(t, CleanupCapacityShrink, ReasonForRelease(allocate.ReleaseCapacityShrink))
	require.Equal(t, CleanupNodeGone, ReasonForRelease(allocate.ReleaseNodeGone))
	require.Equal(t, CleanupNodeIneligible, ReasonForRelease(allocate.ReleaseNodeIneligible))
	require.Equal(t, CleanupSelectorMismatch, ReasonForRelease(allocate.ReleaseSelectorMismatch))
}

func TestCleanupNeededMatchesRackRejectsMissingRack(t *testing.T) {
	t.Parallel()

	needed, _ := boundRack()

	require.False(t, needed.MatchesRack(nil))
}

func boundRack() (CleanupNeeded, *mokkav1alpha1.SGPURack) {
	rack := &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid"},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: "inventory", UID: "inventory-uid"},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 0},
			Nodes: []mokkav1alpha1.SGPURackNode{{
				Index:   0,
				NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"},
			}},
		},
	}
	needed := CleanupNeeded{
		RackName: "rack",
		RackUID:  "rack-uid",
		Reason:   CleanupNodeIneligible,
		Binding: allocate.Binding{
			Coordinate: allocate.Coordinate{
				Group:     allocate.RackGroupKey{InventoryName: "inventory", InventoryUID: "inventory-uid", RackGroup: "group"},
				RackIndex: 0,
				NodeIndex: 0,
			},
			Node: allocate.NodeReference{Name: "node", UID: "node-uid"},
		},
	}
	return needed, rack
}
