// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"cmp"
	"slices"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Coordinate identifies one simulated GPU in an inventory.
type Coordinate struct {
	RackGroup string
	RackIndex int32
	NodeIndex int32
	GPUIndex  int32
}

// layer is one accepted policy's override within a rack group.
type layer struct {
	scope   Scope
	selects selection
	runtime *mokkav1alpha1.RuntimeState
}

// layersByGroup orders each rack group's accepted overrides from the broadest
// scope to the narrowest. Accepted policies of one scope that select the same
// GPU set disjoint fields, so their relative order does not change the result.
func layersByGroup(accepted []candidate) map[string][]layer {
	layers := make(map[string][]layer)
	for _, winner := range accepted {
		for group, selected := range winner.footprint {
			layers[group] = append(layers[group], layer{
				scope:   winner.decision.Scope,
				selects: selected,
				runtime: winner.decision.Policy.Spec.Runtime,
			})
		}
	}
	for _, groupLayers := range layers {
		slices.SortStableFunc(groupLayers, func(a, b layer) int { return cmp.Compare(a.scope, b.scope) })
	}
	return layers
}

// Runtime returns the effective runtime state of the GPU at a coordinate: the
// profile defaults overridden by every accepted policy that selects it, from
// the broadest scope to the narrowest. The caller owns the result.
func (e *Evaluation) Runtime(defaults *mokkav1alpha1.RuntimeState, at Coordinate) mokkav1alpha1.RuntimeState {
	effective := defaults.WithOverride(nil)
	for _, override := range e.layers[at.RackGroup] {
		if override.selects.contains(at) {
			effective = effective.WithOverride(override.runtime)
		}
	}
	return *effective
}
