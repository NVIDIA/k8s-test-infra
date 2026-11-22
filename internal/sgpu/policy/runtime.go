// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"cmp"
	"fmt"
	"iter"
	"slices"

	"k8s.io/apimachinery/pkg/types"

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

// RackPolicies holds one rack group's accepted overrides in precedence order.
type RackPolicies []layer

func (layers RackPolicies) order() {
	slices.SortStableFunc(layers, func(a, b layer) int { return cmp.Compare(a.scope, b.scope) })
}

// DeliveredPolicies resolves exactly the policy versions that the rack publisher accepted.
// A cached policy outside this list cannot displace a published policy.
func DeliveredPolicies(refs []mokkav1alpha1.DeliveryObjectReference, cached map[types.UID]*mokkav1alpha1.SGPURuntimePolicy) (RackPolicies, error) {
	layers := make(RackPolicies, 0, len(refs))
	for _, ref := range refs {
		p := cached[ref.UID]
		if p == nil || p.UID != ref.UID || ref.UID == "" || ref.Generation < 1 || p.Generation != ref.Generation {
			return nil, fmt.Errorf("policy %q generation %d is unavailable", ref.UID, ref.Generation)
		}
		target := &p.Spec.TargetRef
		// The manifest already selects this rack. Only the node and GPU axes
		// can narrow the selection further within it.
		layers = append(layers, layer{
			policy: p,
			scope:  scopeOf(target),
			selects: groupSelection{
				racks: indexes{every: true},
				nodes: deliveredIndexes(target.NodeIndexes),
				gpus:  deliveredIndexes(target.GPUIndexes),
			},
		})
	}
	layers.order()
	return layers, nil
}

func deliveredIndexes(values []int32) indexes {
	return indexes{every: len(values) == 0, listed: sortedCopy(values)}
}

// layersByGroup orders each rack group's accepted overrides from the broadest
// scope to the narrowest. Accepted policies of one scope that select the same
// GPU set disjoint fields, so their relative order does not change the result.
func layersByGroup(accepted []candidate) map[string]RackPolicies {
	layers := make(map[string]RackPolicies)

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
		groupLayers.order()
	}

	return layers
}

// AcceptedForRack yields each accepted policy that selects any GPU in this rack.
// The group layers already exclude targets with no matching node or GPU indices.
func (e *Evaluation) AcceptedForRack(group string, index int32) iter.Seq[*mokkav1alpha1.SGPURuntimePolicy] {
	return func(yield func(*mokkav1alpha1.SGPURuntimePolicy) bool) {
		for _, layer := range e.layers[group] {
			if layer.selects.racks.has(index) && !yield(layer.policy) {
				return
			}
		}
	}
}

// Applied returns the accepted policies that select the GPU at a coordinate,
// in the order their overrides apply: from the broadest scope to the narrowest.
func (e *Evaluation) Applied(at Coordinate) []*mokkav1alpha1.SGPURuntimePolicy {
	return e.layers[at.RackGroup].applied(at)
}

func (layers RackPolicies) applied(at Coordinate) []*mokkav1alpha1.SGPURuntimePolicy {
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
func (e *Evaluation) Runtime(
	defaults *mokkav1alpha1.RuntimeState,
	at Coordinate,
) (mokkav1alpha1.RuntimeState, error) {
	return e.layers[at.RackGroup].Runtime(defaults, at)
}

// Runtime applies these policies to one GPU in the rack.
func (layers RackPolicies) Runtime(defaults *mokkav1alpha1.RuntimeState, at Coordinate) (mokkav1alpha1.RuntimeState, error) {
	applied := layers.applied(at)
	overrides := make([]*mokkav1alpha1.RuntimeState, 0, len(applied))

	for _, policy := range applied {
		overrides = append(overrides, policy.Spec.Runtime)
	}

	runtime, err := defaults.WithOverride(overrides...)
	if err != nil {
		return mokkav1alpha1.RuntimeState{}, err
	}

	return *runtime, nil
}
