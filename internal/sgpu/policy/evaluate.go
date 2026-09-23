// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package policy decides which SGPURuntimePolicies apply to an inventory and
// compiles the effective runtime state of its simulated GPUs. It makes no API
// calls and never modifies its inputs.
package policy

import (
	"cmp"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Decision is the outcome of evaluating one policy.
type Decision struct {
	Policy  *mokkav1alpha1.SGPURuntimePolicy
	Scope   Scope
	Outcome Outcome
	// Message explains the outcome in full sentences.
	Message string
}

// Evaluation holds the decisions for every policy that targets one inventory.
// It is not modified after Evaluate returns, so concurrent readers may share it.
type Evaluation struct {
	InventoryName string
	// InventoryUID is empty when the inventory does not exist.
	InventoryUID types.UID
	// Decisions holds one decision per policy, ordered by policy name.
	Decisions []Decision

	// layers holds each rack group's accepted overrides.
	layers map[string][]layer
}

// Evaluate decides every policy that targets inventoryName. inventory is nil
// when it does not exist, and profiles holds the cached profiles by name; a
// rack group whose profile is absent has no Node or GPU indexes. Invalid and
// conflicting policies are decisions; Evaluate returns an error only when a
// policy's runtime cannot be encoded as JSON.
func Evaluate(
	inventoryName string,
	inventory *mokkav1alpha1.SGPUInventory,
	profiles map[string]*mokkav1alpha1.SGPURackProfile,
	policies []*mokkav1alpha1.SGPURuntimePolicy,
) (*Evaluation, error) {
	evaluation := &Evaluation{
		InventoryName: inventoryName,
		Decisions:     make([]Decision, len(policies)),
	}

	polByName := slices.SortedFunc(slices.Values(policies), func(a, b *mokkav1alpha1.SGPURuntimePolicy) int {
		return cmp.Compare(a.Name, b.Name)
	})

	for i, pol := range polByName {
		evaluation.Decisions[i] = Decision{Policy: pol, Scope: scopeOf(&pol.Spec.TargetRef)}
	}

	if inventory == nil {
		for i := range evaluation.Decisions {
			evaluation.Decisions[i].Outcome = TargetNotFound
			evaluation.Decisions[i].Message = fmt.Sprintf("SGPUInventory %q does not exist.", inventoryName)
		}

		return evaluation, nil
	}

	evaluation.InventoryUID = inventory.UID
	shapes := shapesOf(inventory, profiles)
	candidates := make([]candidate, 0, len(policies))

	for i := range evaluation.Decisions {
		decision := &evaluation.Decisions[i]
		selected, problem := resolveTarget(inventoryName, shapes, &decision.Policy.Spec.TargetRef)

		if problem != "" {
			decision.Outcome, decision.Message = InvalidTarget, problem
			continue
		}

		fields, err := decision.Policy.Spec.Runtime.FieldPaths()
		if err != nil {
			return nil, fmt.Errorf("list the runtime fields of policy %q: %w", decision.Policy.Name, err)
		}

		candidates = append(candidates, candidate{
			decision:  decision,
			selection: selected,
			fields:    fields,
		})
	}

	evaluation.layers = layersByGroup(accept(candidates))

	return evaluation, nil
}
