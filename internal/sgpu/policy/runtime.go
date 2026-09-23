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
	selects groupSelection
	policy  *mokkav1alpha1.SGPURuntimePolicy
}

// layersByGroup orders each rack group's accepted overrides from the broadest
// scope to the narrowest. Accepted policies of one scope that select the same
// GPU set disjoint fields, so their relative order does not change the result.
func layersByGroup(accepted []candidate) map[string][]layer {
	layers := make(map[string][]layer)

	for _, winner := range accepted {
		for group, selected := range winner.selection {
			layers[group] = append(layers[group], layer{
				scope:   winner.decision.Scope,
				selects: selected,
				policy:  winner.decision.Policy,
			})
		}
	}

	for _, groupLayers := range layers {
		slices.SortStableFunc(groupLayers, func(a, b layer) int { return cmp.Compare(a.scope, b.scope) })
	}

	return layers
}

// Applied returns the accepted policies that select the GPU at a coordinate,
// in the order their overrides apply: from the broadest scope to the narrowest.
func (e *Evaluation) Applied(at Coordinate) []*mokkav1alpha1.SGPURuntimePolicy {
	layers := e.layers[at.RackGroup]
	applied := make([]*mokkav1alpha1.SGPURuntimePolicy, 0, len(layers))

	for _, override := range layers {
		if override.selects.contains(at) {
			applied = append(applied, override.policy)
		}
	}

	return applied
}

// Runtime returns the effective runtime state of the GPU at a coordinate: the
// profile defaults overridden by each policy Applied returns, in that order.
// The caller owns the result.
func (e *Evaluation) Runtime(defaults *mokkav1alpha1.RuntimeState, at Coordinate) mokkav1alpha1.RuntimeState {
	effective := defaults.WithOverride(nil)

	for _, policy := range e.Applied(at) {
		effective = effective.WithOverride(policy.Spec.Runtime)
	}

	return *effective
}
