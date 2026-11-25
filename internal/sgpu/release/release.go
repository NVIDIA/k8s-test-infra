// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package release defines why a durable binding is released and the Node
// projection cleanup that must be acknowledged before the release completes.
package release

import (
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
)

// Reason identifies why a durable binding must be released.
type Reason string

// Reasons form one closed vocabulary: Revocable and FreesCapacity classify
// every one of them.
const (
	// CapacityShrink removes bindings outside the newly declared capacity.
	CapacityShrink Reason = "CapacityShrink"
	// CapacityRejected retires an Inventory displaced by aggregate admission.
	CapacityRejected Reason = "CapacityRejected"
	GroupRemoved     Reason = "GroupRemoved"
	NodeIneligible   Reason = "NodeIneligible"
	// NodeGone removes bindings whose Node left the eligible Node set.
	NodeGone          Reason = "NodeGone"
	SelectorMismatch  Reason = "SelectorMismatch"
	RackDeleting      Reason = "RackDeleting"
	InventoryDeleting Reason = "InventoryDeleting"
)

// Revocable reports whether a later allocation can make the binding desired
// again, so the cleanup must be re-checked against the current allocation
// before it completes.
func (r Reason) Revocable() bool {
	switch r {
	case CapacityShrink, CapacityRejected, GroupRemoved,
		NodeIneligible, NodeGone, SelectorMismatch:
		return true
	case RackDeleting, InventoryDeleting:
		return false
	}
	return false
}

// FreesCapacity reports whether the cleanup is part of retiring or shrinking
// racks, which frees durable capacity, rather than releasing one binding in a
// rack that stays.
func (r Reason) FreesCapacity() bool {
	switch r {
	case CapacityShrink, CapacityRejected, GroupRemoved,
		RackDeleting, InventoryDeleting:
		return true
	case NodeIneligible, SelectorMismatch, NodeGone:
		return false
	}
	return false
}

// ReasonFor returns the release reason for a binding the allocator released.
func ReasonFor(reason allocate.ReleaseReason) Reason {
	switch reason {
	case allocate.ReleaseGroupRemoved:
		return GroupRemoved
	case allocate.ReleaseCapacityShrink:
		return CapacityShrink
	case allocate.ReleaseNodeGone:
		return NodeGone
	case allocate.ReleaseNodeIneligible:
		return NodeIneligible
	case allocate.ReleaseSelectorMismatch:
		return SelectorMismatch
	}
	return Reason(reason)
}

// Cleanup is the exact binding whose Node projection must be removed before
// reconciliation may clear or retire its rack coordinate.
type Cleanup struct {
	AssignmentRevision int64
	RackName           string
	RackUID            types.UID
	Binding            allocate.Binding
	Reason             Reason
}

// ForSlot captures the assignment being released while the slot is still bound.
func ForSlot(rack *mokkav1alpha1.SGPURack, slot mokkav1alpha1.SGPURackNode, reason Reason) Cleanup {
	return Cleanup{
		RackName:           rack.Name,
		RackUID:            rack.UID,
		AssignmentRevision: slot.NodeRef.AssignmentRevision,
		Reason:             reason,
		Binding: allocate.Binding{
			Coordinate: allocate.Coordinate{
				Group: allocate.RackGroupKey{
					InventoryName: rack.Spec.InventoryRef.Name,
					InventoryUID:  rack.Spec.InventoryRef.UID,
					RackGroup:     rack.Spec.Identity.RackGroup,
				},
				RackIndex: rack.Spec.Identity.RackIndex,
				NodeIndex: slot.Index,
			},
			Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
		},
	}
}

// MatchesRack reports whether rack still holds the exact binding this cleanup
// retires: the same rack instance and coordinate, bound to the same Node.
func (c Cleanup) MatchesRack(rack *mokkav1alpha1.SGPURack) bool {
	ref := c.BoundNode(rack)
	return ref != nil && ref.AssignmentRevision == c.AssignmentRevision
}

// BoundNode identifies the Node at the cleanup coordinate even if it has since
// acquired a new assignment. Old v1 Node annotations do not carry a revision.
func (c Cleanup) BoundNode(rack *mokkav1alpha1.SGPURack) *mokkav1alpha1.SGPUNodeReference {
	binding := c.Binding
	if rack == nil || rack.Name != c.RackName || rack.UID != c.RackUID ||
		rack.Spec.InventoryRef.Name != binding.Coordinate.Group.InventoryName ||
		rack.Spec.InventoryRef.UID != binding.Coordinate.Group.InventoryUID ||
		rack.Spec.Identity.RackGroup != binding.Coordinate.Group.RackGroup ||
		rack.Spec.Identity.RackIndex != binding.Coordinate.RackIndex {
		return nil
	}
	slot := rack.Spec.NodeByIndex(binding.Coordinate.NodeIndex)
	if slot == nil || !slot.BoundTo(binding.Node.Name, binding.Node.UID) {
		return nil
	}
	return slot.NodeRef
}

// Gate is the acknowledgement seam implemented by Node projection.
// An acknowledgement remains ready until the rack cache observes that the
// exact binding is gone, so stale reconciles cannot restore a cleaned binding.
type Gate interface {
	Ready(Cleanup) bool
}

// GateFunc adapts a function to Gate.
type GateFunc func(Cleanup) bool

// Ready delegates an exact cleanup query to the wrapped function.
func (f GateFunc) Ready(cleanup Cleanup) bool { return f(cleanup) }
