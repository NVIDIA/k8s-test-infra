// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// candidate is a valid policy awaiting conflict resolution.
type candidate struct {
	decision  *Decision
	selection selection
	fields    []string
}

// accept settles conflicts oldest policy first: a policy is accepted unless an
// already accepted policy of the same scope sets one of its fields for some of
// the same GPUs. A rejected policy therefore never blocks a younger one, and
// once a winner is deleted the oldest remaining challenger takes its place.
func accept(candidates []candidate) []candidate {
	slices.SortFunc(candidates, func(a, b candidate) int {
		return comparePrecedence(a.decision.Policy, b.decision.Policy)
	})
	accepted := make([]candidate, 0, len(candidates))
	for _, challenger := range candidates {
		if winner, shared := firstConflict(accepted, challenger); winner != nil {
			challenger.decision.Outcome = Conflicted
			challenger.decision.Message = fmt.Sprintf(
				"Policy %q takes precedence at %s scope and also sets %s for some of the same GPUs.",
				winner.Name, challenger.decision.Scope, strings.Join(shared, ", "),
			)
			continue
		}
		challenger.decision.Outcome = Accepted
		challenger.decision.Message = fmt.Sprintf("The policy applies at %s scope.", challenger.decision.Scope)
		accepted = append(accepted, challenger)
	}
	return accepted
}

// firstConflict returns the oldest accepted policy that conflicts with the
// challenger, together with the fields both of them set.
func firstConflict(accepted []candidate, challenger candidate) (*mokkav1alpha1.SGPURuntimePolicy, []string) {
	for _, incumbent := range accepted {
		if incumbent.decision.Scope != challenger.decision.Scope {
			continue
		}
		shared := sharedFields(incumbent.fields, challenger.fields)
		if len(shared) > 0 && incumbent.selection.overlaps(challenger.selection) {
			return incumbent.decision.Policy, shared
		}
	}
	return nil, nil
}

// comparePrecedence orders policies oldest first. Creation timestamps have
// one-second granularity, so the UID breaks the frequent ties.
func comparePrecedence(a, b *mokkav1alpha1.SGPURuntimePolicy) int {
	if order := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); order != 0 {
		return order
	}
	return cmp.Compare(a.UID, b.UID)
}

// sharedFields returns the field paths present in both sorted lists.
func sharedFields(a, b []string) []string {
	return slices.DeleteFunc(slices.Clone(a), func(field string) bool {
		_, found := slices.BinarySearch(b, field)
		return !found
	})
}
