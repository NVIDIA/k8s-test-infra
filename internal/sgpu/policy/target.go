// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// resolveTarget decides which GPUs a target selects in an inventory. A target
// that omits rack groups selects every declared one. The rules apply in order:
//  1. every listed rack group must be declared by the inventory;
//  2. every listed index must select at least one GPU: it must exist in a
//     selected rack group that also has an index of every other listed axis.
//
// Each rack group applies only the listed indexes it has, so a target can
// span rack groups of different shapes. resolveTarget returns what the target
// selects in each rack group it reaches, or a message explaining why the
// target is invalid.
func resolveTarget(
	inventoryName string,
	shapes map[string]shape,
	target *mokkav1alpha1.PolicyTargetRef,
) (footprint, string) {
	groups := target.RackGroups
	if len(groups) == 0 {
		groups = slices.Sorted(maps.Keys(shapes))
	}
	undeclared := slices.DeleteFunc(slices.Clone(groups), func(group string) bool {
		_, declared := shapes[group]
		return declared
	})
	if len(undeclared) > 0 {
		return nil, fmt.Sprintf("SGPUInventory %q does not declare rack groups %s.", inventoryName, quoted(undeclared))
	}

	racks, nodes, gpus := sortedCopy(target.RackIndexes), sortedCopy(target.NodeIndexes), sortedCopy(target.GPUIndexes)
	selected := make(footprint, len(groups))
	var reach shape
	for _, group := range groups {
		groupShape := shapes[group]
		groupSelection := selection{
			racks: within(racks, groupShape.racks),
			nodes: within(nodes, groupShape.nodes),
			gpus:  within(gpus, groupShape.gpus),
		}
		if groupSelection.selectsAny() {
			selected[group] = groupSelection
			reach.racks = max(reach.racks, groupShape.racks)
			reach.nodes = max(reach.nodes, groupShape.nodes)
			reach.gpus = max(reach.gpus, groupShape.gpus)
		}
	}

	// A listed index is used when it lies below the largest bound of the rack
	// groups the target reaches, because every rack group counts from zero.
	unusedNodesOrGPUs := slices.Concat(outside("nodeIndexes", nodes, reach.nodes), outside("gpuIndexes", gpus, reach.gpus))
	unused := slices.Concat(outside("rackIndexes", racks, reach.racks), unusedNodesOrGPUs)
	if len(unused) == 0 {
		return selected, ""
	}
	var unresolved []string
	if len(unusedNodesOrGPUs) > 0 {
		unresolved = slices.DeleteFunc(slices.Clone(groups), func(group string) bool {
			return shapes[group].profileResolved
		})
	}
	return nil, unusedIndexesMessage(unused, unresolved)
}

// outside describes the listed indexes of one axis that lie outside [0, reach).
func outside(axis string, sorted []int32, reach int32) []string {
	low, _ := slices.BinarySearch(sorted, 0)
	high, _ := slices.BinarySearch(sorted, reach)
	if unused := slices.Concat(sorted[:low], sorted[high:]); len(unused) > 0 {
		return []string{fmt.Sprintf("%s %v", axis, unused)}
	}
	return nil
}

func unusedIndexesMessage(unused, unresolvedGroups []string) string {
	message := "The target lists indexes that select no GPU in the selected rack groups: " +
		strings.Join(unused, ", ") + "."
	if len(unresolvedGroups) == 0 {
		return message
	}
	return message + " The profiles of rack groups " + quoted(unresolvedGroups) +
		" are not found, so those rack groups select no Node or GPU."
}

// sortedCopy sorts a copy because the target belongs to the informer cache.
func sortedCopy(values []int32) []int32 {
	return slices.Sorted(slices.Values(values))
}

func quoted(values []string) string {
	quotedValues := make([]string, len(values))
	for i, value := range values {
		quotedValues[i] = strconv.Quote(value)
	}
	return strings.Join(quotedValues, ", ")
}
