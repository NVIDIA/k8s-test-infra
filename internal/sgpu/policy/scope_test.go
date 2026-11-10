// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"testing"

	"github.com/stretchr/testify/require"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestScopeIsTheDeepestListedTargetAxis(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target mokkav1alpha1.PolicyTargetRef
		want   Scope
	}{
		{name: "no axis covers the inventory", want: ScopeInventory},
		{name: "rack groups", target: mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"training"}}, want: ScopeRackGroup},
		{name: "rack indexes without rack groups", target: mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{0}}, want: ScopeRack},
		{name: "Node indexes without rack indexes", target: mokkav1alpha1.PolicyTargetRef{NodeIndexes: []int32{0}}, want: ScopeNode},
		{name: "GPU indexes alone", target: mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{0}}, want: ScopeGPU},
		{
			name: "every axis",
			target: mokkav1alpha1.PolicyTargetRef{
				RackGroups: []string{"training"}, RackIndexes: []int32{0}, NodeIndexes: []int32{0}, GPUIndexes: []int32{0},
			},
			want: ScopeGPU,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, scopeOf(&tt.target))
		})
	}
}
