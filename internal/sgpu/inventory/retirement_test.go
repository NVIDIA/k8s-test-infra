// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpucleanup "github.com/NVIDIA/k8s-test-infra/internal/sgpu/cleanup"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
)

func TestRetirementReasonDecidesWhichOwnedRacksRetire(t *testing.T) {
	t.Parallel()

	inventory := &mokkav1alpha1.SGPUInventory{ObjectMeta: metav1.ObjectMeta{Name: "inventory", UID: "inventory-uid"}}
	groups := map[string]resolvedGroup{"group": {group: mokkav1alpha1.RackGroup{ID: "group", Count: 2}}}
	unresolved := map[string]struct{}{"unresolved": {}}
	rack := func(group string, index int32) *mokkav1alpha1.SGPURack {
		return &mokkav1alpha1.SGPURack{
			ObjectMeta: metav1.ObjectMeta{Name: rackrender.RackName(inventory.Name, inventory.UID, group, index)},
			Spec: mokkav1alpha1.SGPURackSpec{
				Identity: mokkav1alpha1.SGPURackIdentity{RackGroup: group, RackIndex: index},
			},
		}
	}
	deleting := rack("group", 0)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	renamed := rack("group", 1)
	renamed.Name = "renamed"

	tests := []struct {
		name   string
		rack   *mokkav1alpha1.SGPURack
		reason sgpucleanup.CleanupReason
		retire bool
	}{
		{name: "declared", rack: rack("group", 1)},
		{name: "profile unresolved", rack: rack("unresolved", 0)},
		{name: "being deleted", rack: deleting, reason: sgpucleanup.CleanupRackDeleting, retire: true},
		{name: "beyond group count", rack: rack("group", 2), reason: sgpucleanup.CleanupCapacityShrink, retire: true},
		{name: "group removed", rack: rack("removed", 0), reason: sgpucleanup.CleanupGroupRemoved, retire: true},
		{name: "not canonical for its coordinate", rack: renamed, reason: sgpucleanup.CleanupGroupRemoved, retire: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reason, retire := retirementReason(inventory, tt.rack, groups, unresolved)
			require.Equal(t, tt.retire, retire)
			require.Equal(t, tt.reason, reason)
		})
	}
}
