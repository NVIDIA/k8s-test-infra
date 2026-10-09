// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestResolveTargetSelectsIndexesWhereTheyExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		target      mokkav1alpha1.PolicyTargetRef
		wantGroups  []string
		wantProblem string
	}{
		{
			name:       "inventory scope selects every declared rack group",
			wantGroups: []string{"inference", "pending", "training"},
		},
		{
			name:        "listed rack group must be declared",
			target:      mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"training", "ghost", "phantom"}},
			wantProblem: `SGPUInventory "dev" does not declare rack groups "ghost", "phantom".`,
		},
		{
			name:       "rack index selects only the rack groups that have it",
			target:     mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{3}},
			wantGroups: []string{"training"},
		},
		{
			name:        "rack index beyond every selected rack group is invalid",
			target:      mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"inference"}, RackIndexes: []int32{2, 1, 5}},
			wantProblem: "The target lists indexes that select no GPU in the selected rack groups: rackIndexes [2 5].",
		},
		{
			name: "indexes that exist only in different rack groups are invalid together",
			target: mokkav1alpha1.PolicyTargetRef{
				RackGroups: []string{"training", "inference"}, RackIndexes: []int32{3}, GPUIndexes: []int32{6},
			},
			wantProblem: "The target lists indexes that select no GPU in the selected rack groups: " +
				"rackIndexes [3], gpuIndexes [6].",
		},
		{
			name:       "rack group without a listed index contributes nothing",
			target:     mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"training", "inference"}, NodeIndexes: []int32{5}},
			wantGroups: []string{"training"},
		},
		{
			name:       "rack index needs no resolved profile",
			target:     mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"pending"}, RackIndexes: []int32{2}},
			wantGroups: []string{"pending"},
		},
		{
			name:   "Node index needs a resolved profile",
			target: mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"pending"}, NodeIndexes: []int32{0}},
			wantProblem: "The target lists indexes that select no GPU in the selected rack groups: nodeIndexes [0]. " +
				`The profiles of rack groups "pending" are not found, so those rack groups select no Node or GPU.`,
		},
		{
			name:        "negative index is invalid",
			target:      mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"training"}, GPUIndexes: []int32{0, -1}},
			wantProblem: "The target lists indexes that select no GPU in the selected rack groups: gpuIndexes [-1].",
		},
	}
	shapes := shapesOf(testInventory(), testProfiles())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			selected, problem := resolveTarget("dev", shapes, &tt.target)
			require.Equal(t, tt.wantProblem, problem)
			require.Equal(t, tt.wantGroups, slices.Sorted(maps.Keys(selected)))
		})
	}
}
