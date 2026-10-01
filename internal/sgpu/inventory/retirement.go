// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package inventory

import (
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
	sgpurelease "github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
)

// retirementReason decides whether full reconciliation retires an owned rack,
// and why. A rack being deleted retires as RackDeleting. A rack in a group
// whose profile does not resolve is kept, so the group's last good racks stay
// until it resolves again. A rack the Inventory still declares is kept: its
// group exists, its index is within the group's count, and it carries the
// canonical name for that coordinate. Any other rack retires as CapacityShrink
// when its index is beyond its group's count, and as GroupRemoved otherwise.
func retirementReason(
	inventory *mokkav1alpha1.SGPUInventory,
	rack *mokkav1alpha1.SGPURack,
	groups map[string]resolvedGroup,
	unresolved map[string]struct{},
) (sgpurelease.Reason, bool) {
	if rack.DeletionTimestamp != nil {
		return sgpurelease.RackDeleting, true
	}
	identity := rack.Spec.Identity
	if _, keepLastGood := unresolved[identity.RackGroup]; keepLastGood {
		return "", false
	}
	group, exists := groups[identity.RackGroup]
	switch {
	case !exists:
		return sgpurelease.GroupRemoved, true
	case identity.RackIndex >= group.group.Count:
		return sgpurelease.CapacityShrink, true
	case identity.RackIndex < 0 ||
		rack.Name != rackrender.RackName(inventory.Name, inventory.UID, group.group.ID, identity.RackIndex):
		return sgpurelease.GroupRemoved, true
	default:
		return "", false
	}
}
