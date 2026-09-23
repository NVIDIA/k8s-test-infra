// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestEvaluateAcceptsTheOldestOfConflictingPolicies(t *testing.T) {
	t.Parallel()

	training := mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"training"}}
	withUID := func(policy *mokkav1alpha1.SGPURuntimePolicy, uid types.UID) *mokkav1alpha1.SGPURuntimePolicy {
		policy.UID = uid
		return policy
	}
	tests := []struct {
		name     string
		policies []*mokkav1alpha1.SGPURuntimePolicy
		want     map[string]Outcome
	}{
		{
			name: "older policy keeps a field of the GPUs both select",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("younger", 2, training, coolGPU()),
				testPolicy("older", 1, training, hotGPU()),
			},
			want: map[string]Outcome{"older": Accepted, "younger": Conflicted},
		},
		{
			name: "UID breaks a creation timestamp tie",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				withUID(testPolicy("a", 1, training, hotGPU()), "uid-2"),
				withUID(testPolicy("b", 1, training, coolGPU()), "uid-1"),
			},
			want: map[string]Outcome{"a": Conflicted, "b": Accepted},
		},
		{
			name: "disjoint racks do not conflict",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("rack-0", 1, mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{0}}, hotGPU()),
				testPolicy("rack-1", 2, mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{1}}, coolGPU()),
			},
			want: map[string]Outcome{"rack-0": Accepted, "rack-1": Accepted},
		},
		{
			name: "different rack groups do not conflict",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("training", 1, training, hotGPU()),
				testPolicy("inference", 2, mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"inference"}}, coolGPU()),
			},
			want: map[string]Outcome{"training": Accepted, "inference": Accepted},
		},
		{
			name: "different fields do not conflict",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("hot", 1, training, hotGPU()),
				testPolicy("failed", 2, training, failedGPU()),
			},
			want: map[string]Outcome{"hot": Accepted, "failed": Accepted},
		},
		{
			name: "different scopes do not conflict",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("inventory", 1, mokkav1alpha1.PolicyTargetRef{}, hotGPU()),
				testPolicy("training", 2, training, coolGPU()),
			},
			want: map[string]Outcome{"inventory": Accepted, "training": Accepted},
		},
		{
			name: "invalid policy blocks nothing",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("ghost", 1, mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"ghost"}}, hotGPU()),
				testPolicy("valid", 2, mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"inference"}}, coolGPU()),
			},
			want: map[string]Outcome{"ghost": InvalidTarget, "valid": Accepted},
		},
		{
			name: "policy blocked only by a rejected challenger is accepted",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("rack-0", 1, mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{0}}, hotGPU()),
				testPolicy("racks-0-1", 2, mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{0, 1}}, coolGPU()),
				testPolicy("rack-1", 3, mokkav1alpha1.PolicyTargetRef{RackIndexes: []int32{1}}, hotGPU()),
			},
			want: map[string]Outcome{"rack-0": Accepted, "racks-0-1": Conflicted, "rack-1": Accepted},
		},
		{
			name: "policy without runtime is accepted and blocks nothing",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("empty", 1, training, nil),
				testPolicy("hot", 2, training, hotGPU()),
			},
			want: map[string]Outcome{"empty": Accepted, "hot": Accepted},
		},
		{
			// Rack 3 exists only in training, which has 4 GPUs, and GPU 6 exists
			// only in inference, which has 2 racks, so the targets share no GPU
			// even though their listed indexes intersect.
			name: "indexes that coincide only outside every rack group's shape do not conflict",
			policies: []*mokkav1alpha1.SGPURuntimePolicy{
				testPolicy("first", 1, mokkav1alpha1.PolicyTargetRef{
					RackGroups: []string{"training", "inference"}, RackIndexes: []int32{1, 3}, GPUIndexes: []int32{1, 6},
				}, hotGPU()),
				testPolicy("second", 2, mokkav1alpha1.PolicyTargetRef{
					RackGroups: []string{"training", "inference"}, RackIndexes: []int32{0, 3}, GPUIndexes: []int32{0, 6},
				}, coolGPU()),
			},
			want: map[string]Outcome{"first": Accepted, "second": Accepted},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, outcomes(Evaluate("dev", testInventory(), testProfiles(), tt.policies)))
		})
	}
}

func TestEvaluatePromotesTheOldestRemainingChallengerWhenTheWinnerIsDeleted(t *testing.T) {
	t.Parallel()

	winner := testPolicy("winner", 1, mokkav1alpha1.PolicyTargetRef{}, hotGPU())
	next := testPolicy("next", 2, mokkav1alpha1.PolicyTargetRef{}, coolGPU())
	last := testPolicy("last", 3, mokkav1alpha1.PolicyTargetRef{}, hotGPU())

	before := Evaluate("dev", testInventory(), testProfiles(), []*mokkav1alpha1.SGPURuntimePolicy{winner, next, last})
	require.Equal(t, map[string]Outcome{"winner": Accepted, "next": Conflicted, "last": Conflicted}, outcomes(before))

	after := Evaluate("dev", testInventory(), testProfiles(), []*mokkav1alpha1.SGPURuntimePolicy{next, last})
	require.Equal(t, map[string]Outcome{"next": Accepted, "last": Conflicted}, outcomes(after))
}

func TestConflictMessageNamesTheWinnerAndTheSharedFields(t *testing.T) {
	t.Parallel()

	winner := testPolicy("winner", 1, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{0}}, &mokkav1alpha1.RuntimeState{
		DeviceState: mokkav1alpha1.DeviceStateFailed,
		Telemetry: &mokkav1alpha1.RuntimeTelemetry{
			Temperature: &mokkav1alpha1.TemperatureTelemetry{GPUCelsius: ptr.To[int32](90)},
		},
	})
	challenger := testPolicy("challenger", 2, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{0, 1}}, &mokkav1alpha1.RuntimeState{
		DeviceState: mokkav1alpha1.DeviceStateDegraded,
		Modes:       &mokkav1alpha1.RuntimeModes{ECC: "Disabled"},
		Telemetry: &mokkav1alpha1.RuntimeTelemetry{
			Temperature: &mokkav1alpha1.TemperatureTelemetry{GPUCelsius: ptr.To[int32](40)},
		},
	})

	evaluation := Evaluate("dev", testInventory(), testProfiles(), []*mokkav1alpha1.SGPURuntimePolicy{winner, challenger})

	require.Equal(t, []Decision{
		{
			Policy: challenger, Scope: ScopeGPU, Outcome: Conflicted,
			Message: `Policy "winner" takes precedence at GPU scope and also sets deviceState, ` +
				"telemetry.temperature.gpuCelsius for some of the same GPUs.",
		},
		{Policy: winner, Scope: ScopeGPU, Outcome: Accepted, Message: "The policy applies at GPU scope."},
	}, evaluation.Decisions)
}
