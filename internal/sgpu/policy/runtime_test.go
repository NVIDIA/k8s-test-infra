// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestEvaluationRuntimeComposesAcceptedPoliciesFieldByField(t *testing.T) {
	t.Parallel()

	training := []string{"training"}
	evaluation := Evaluate("dev", testInventory(), testProfiles(), []*mokkav1alpha1.SGPURuntimePolicy{
		testPolicy("inventory-warm", 1, mokkav1alpha1.PolicyTargetRef{}, gpuCelsius(50)),
		testPolicy("training-unpowered", 2, mokkav1alpha1.PolicyTargetRef{RackGroups: training},
			&mokkav1alpha1.RuntimeState{Telemetry: &mokkav1alpha1.RuntimeTelemetry{
				Power: &mokkav1alpha1.PowerTelemetry{DrawMilliWatts: ptr.To[int64](0)},
			}}),
		testPolicy("rack-1-hot", 3, mokkav1alpha1.PolicyTargetRef{RackGroups: training, RackIndexes: []int32{1}}, gpuCelsius(70)),
		testPolicy("gpu-2-failed", 4, mokkav1alpha1.PolicyTargetRef{RackGroups: training, GPUIndexes: []int32{2}}, failedGPU()),
		testPolicy("node-0-ecc-off", 5, mokkav1alpha1.PolicyTargetRef{NodeIndexes: []int32{0}},
			&mokkav1alpha1.RuntimeState{Modes: &mokkav1alpha1.RuntimeModes{ECC: "Disabled"}}),
		testPolicy("inventory-conflicted", 6, mokkav1alpha1.PolicyTargetRef{}, gpuCelsius(99)),
	})
	require.Equal(t, Conflicted, outcomes(evaluation)["inventory-conflicted"])

	state := func(
		device mokkav1alpha1.DeviceState, celsius int32, milliwatts int64, ecc string,
	) mokkav1alpha1.RuntimeState {
		want := testDefaults()
		want.DeviceState = device
		want.Telemetry.Temperature.GPUCelsius = ptr.To(celsius)
		want.Telemetry.Power.DrawMilliWatts = ptr.To(milliwatts)
		if ecc != "" {
			want.Modes = &mokkav1alpha1.RuntimeModes{ECC: ecc}
		}
		return *want
	}
	healthy, failed := mokkav1alpha1.DeviceStateHealthy, mokkav1alpha1.DeviceStateFailed
	tests := []struct {
		name string
		at   Coordinate
		want mokkav1alpha1.RuntimeState
	}{
		{
			name: "inventory scope overrides the profile defaults",
			at:   Coordinate{RackGroup: "inference", RackIndex: 0, NodeIndex: 1, GPUIndex: 2},
			want: state(healthy, 50, 175000, ""),
		},
		{
			name: "narrower scopes override only the fields they set, explicit zero included",
			at:   Coordinate{RackGroup: "training", RackIndex: 1, NodeIndex: 1, GPUIndex: 0},
			want: state(healthy, 70, 0, ""),
		},
		{
			name: "GPU scope applies only to the GPUs it selects",
			at:   Coordinate{RackGroup: "training", RackIndex: 0, NodeIndex: 1, GPUIndex: 2},
			want: state(failed, 50, 0, ""),
		},
		{
			name: "every accepted policy that selects the GPU contributes its fields",
			at:   Coordinate{RackGroup: "training", RackIndex: 1, NodeIndex: 0, GPUIndex: 2},
			want: state(failed, 70, 0, "Disabled"),
		},
		{
			name: "broad policies reach a retiring rack beyond the declared count",
			at:   Coordinate{RackGroup: "training", RackIndex: 7, NodeIndex: 1, GPUIndex: 0},
			want: state(healthy, 50, 0, ""),
		},
		{
			name: "rack group the inventory no longer declares keeps the profile defaults",
			at:   Coordinate{RackGroup: "retired", RackIndex: 0, NodeIndex: 0, GPUIndex: 0},
			want: *testDefaults(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, evaluation.Runtime(testDefaults(), tt.at))
		})
	}

	require.Equal(t, *gpuCelsius(50), evaluation.Runtime(nil, Coordinate{RackGroup: "inference", NodeIndex: 1}),
		"without profile defaults the policies alone make up the state")
}

func TestEvaluationRuntimeIsOwnedByTheCaller(t *testing.T) {
	t.Parallel()

	policy := testPolicy("hot", 1, mokkav1alpha1.PolicyTargetRef{}, hotGPU())
	defaults := testDefaults()
	evaluation := Evaluate("dev", testInventory(), testProfiles(), []*mokkav1alpha1.SGPURuntimePolicy{policy})

	state := evaluation.Runtime(defaults, Coordinate{RackGroup: "training"})
	*state.Telemetry.Temperature.GPUCelsius = 1
	*state.Telemetry.Power.DrawMilliWatts = 1

	require.Equal(t, hotGPU(), policy.Spec.Runtime)
	require.Equal(t, testDefaults(), defaults)
}
