// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"slices"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// shape is the extent of one rack group. Its rack count comes from the
// inventory; its Node and GPU counts come from the group's profile and stay
// zero while the profile is not in the cache.
type shape struct {
	racks, nodes, gpus int32
	profileResolved    bool
}

func shapesOf(
	inventory *mokkav1alpha1.SGPUInventory,
	profiles map[string]*mokkav1alpha1.SGPURackProfile,
) map[string]shape {
	shapes := make(map[string]shape, len(inventory.Spec.RackGroups))

	for _, group := range inventory.Spec.RackGroups {
		groupShape := shape{racks: group.Count}
		if profile := profiles[group.ProfileRef.Name]; profile != nil {
			groupShape.nodes = profile.Spec.Rack.NodesPerRack
			groupShape.gpus = profile.Spec.Node.GPUs.Count
			groupShape.profileResolved = true
		}
		shapes[group.ID] = groupShape
	}

	return shapes
}

// indexes is what one target axis selects within one rack group.
type indexes struct {
	// every is set when the target does not list the axis.
	every bool
	// listed holds the sorted listed indexes that exist in the rack group.
	listed []int32
}

// within restricts a sorted listed axis to the indexes below bound. An
// unlisted axis selects every index, including those of racks still retiring
// beyond a shrunk rack count.
func within(sorted []int32, bound int32) indexes {
	if len(sorted) == 0 {
		return indexes{every: true}
	}

	low, _ := slices.BinarySearch(sorted, 0)
	high, _ := slices.BinarySearch(sorted, bound)

	return indexes{listed: sorted[low:high]}
}

func (x indexes) selectsAny() bool {
	return x.every || len(x.listed) > 0
}

func (x indexes) has(index int32) bool {
	if x.every {
		return true
	}
	_, found := slices.BinarySearch(x.listed, index)
	return found
}

func (x indexes) overlaps(y indexes) bool {
	switch {
	case x.every:
		return y.selectsAny()
	case y.every:
		return x.selectsAny()
	default:
		return slices.ContainsFunc(x.listed, y.has)
	}
}

// groupSelection is what a target selects within one rack group.
type groupSelection struct {
	racks, nodes, gpus indexes
}

func (s groupSelection) selectsAny() bool {
	return s.racks.selectsAny() && s.nodes.selectsAny() && s.gpus.selectsAny()
}

func (s groupSelection) contains(at Coordinate) bool {
	return s.racks.has(at.RackIndex) && s.nodes.has(at.NodeIndex) && s.gpus.has(at.GPUIndex)
}

func (s groupSelection) overlaps(other groupSelection) bool {
	return s.racks.overlaps(other.racks) && s.nodes.overlaps(other.nodes) && s.gpus.overlaps(other.gpus)
}

// selection is what a target selects in each rack group it reaches. Two
// selections overlap only when they select a common GPU, so listed indexes
// that coincide outside every rack group's shape never make policies collide.
type selection map[string]groupSelection

func (s selection) overlaps(other selection) bool {
	for group, selected := range s {
		if otherSelected, ok := other[group]; ok && selected.overlaps(otherSelected) {
			return true
		}
	}

	return false
}
