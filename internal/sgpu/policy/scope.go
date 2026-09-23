// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"

// Scope is how much of an inventory a policy target covers. Scopes are ordered
// from the broadest to the narrowest, and a narrower scope overrides a broader
// one field by field.
type Scope int

// Scopes form one closed vocabulary: String names every one of them.
const (
	ScopeInventory Scope = iota
	ScopeRackGroup
	ScopeRack
	ScopeNode
	ScopeGPU
)

// scopeOf returns the scope of the deepest axis a target lists.
func scopeOf(target *mokkav1alpha1.PolicyTargetRef) Scope {
	switch {
	case len(target.GPUIndexes) > 0:
		return ScopeGPU
	case len(target.NodeIndexes) > 0:
		return ScopeNode
	case len(target.RackIndexes) > 0:
		return ScopeRack
	case len(target.RackGroups) > 0:
		return ScopeRackGroup
	default:
		return ScopeInventory
	}
}

// String names the scope in status messages.
func (s Scope) String() string {
	switch s {
	case ScopeInventory:
		return "Inventory"
	case ScopeRackGroup:
		return "Rack Group"
	case ScopeRack:
		return "Rack"
	case ScopeNode:
		return "Node"
	case ScopeGPU:
		return "GPU"
	}
	return "Unknown"
}
