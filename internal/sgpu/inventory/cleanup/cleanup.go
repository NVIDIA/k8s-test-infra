// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package cleanup defines the handoff protocol for removing Node projections.
package cleanup

import (
	"k8s.io/apimachinery/pkg/types"

	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/allocate"
)

// CleanupReason identifies why a durable binding must be removed.
//
//nolint:revive // The established name is preserved while moving package ownership.
type CleanupReason string

//nolint:revive // Cleanup reasons form one closed rack lifecycle vocabulary.
const (
	// CleanupCapacityShrink removes bindings outside the newly declared capacity.
	CleanupCapacityShrink CleanupReason = "CapacityShrink"
	// CleanupCapacityRejected retires an Inventory displaced by aggregate admission.
	CleanupCapacityRejected  CleanupReason = "CapacityRejected"
	CleanupGroupRemoved      CleanupReason = "GroupRemoved"
	CleanupNodeIneligible    CleanupReason = "NodeIneligible"
	CleanupSelectorMismatch  CleanupReason = "SelectorMismatch"
	CleanupRackDeleting      CleanupReason = "RackDeleting"
	CleanupInventoryDeleting CleanupReason = "InventoryDeleting"
)

// CleanupNeeded is the exact binding whose Node projection must be removed
// before reconciliation may clear or retire its rack coordinate.
//
//nolint:revive // The established name is preserved while moving package ownership.
type CleanupNeeded struct {
	RackName string
	RackUID  types.UID
	Binding  allocate.Binding
	Reason   CleanupReason
}

// CleanupGate is the acknowledgement seam implemented by Node projection.
// An acknowledgement remains ready until the rack cache observes that the
// exact binding is gone, so stale reconciles cannot restore a cleaned binding.
//
//nolint:revive // The established name is preserved while moving package ownership.
type CleanupGate interface {
	Ready(CleanupNeeded) bool
}

// CleanupGateFunc adapts a function to CleanupGate.
//
//nolint:revive // The established name is preserved while moving package ownership.
type CleanupGateFunc func(CleanupNeeded) bool

// Ready delegates an exact cleanup query to the wrapped function.
func (f CleanupGateFunc) Ready(cleanup CleanupNeeded) bool { return f(cleanup) }
