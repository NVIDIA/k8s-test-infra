// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestEvaluateRejectsEveryPolicyOfAMissingInventory(t *testing.T) {
	t.Parallel()

	second := testPolicy("second", 2, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{0}}, hotGPU())
	first := testPolicy("first", 1, mokkav1alpha1.PolicyTargetRef{}, hotGPU())

	evaluation := mustEvaluate(t, "dev", nil, testProfiles(), []*mokkav1alpha1.SGPURuntimePolicy{second, first})

	require.Empty(t, evaluation.InventoryUID)
	require.Equal(t, []Decision{
		{Policy: first, Scope: ScopeInventory, Outcome: TargetNotFound, Message: `SGPUInventory "dev" does not exist.`},
		{Policy: second, Scope: ScopeGPU, Outcome: TargetNotFound, Message: `SGPUInventory "dev" does not exist.`},
	}, evaluation.Decisions)
	require.Equal(t, mokkav1alpha1.RuntimeState{}, mustRuntime(t, evaluation, nil, Coordinate{RackGroup: "training"}))
}

func TestEvaluateIsDeterministicForAnyPolicyOrder(t *testing.T) {
	t.Parallel()

	policies := []*mokkav1alpha1.SGPURuntimePolicy{
		testPolicy("inventory-hot", 1, mokkav1alpha1.PolicyTargetRef{}, hotGPU()),
		testPolicy("inventory-cool", 2, mokkav1alpha1.PolicyTargetRef{}, coolGPU()),
		testPolicy("training-failed", 3, mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"training"}}, failedGPU()),
		testPolicy("gpu-hot", 4, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{1, 3}}, hotGPU()),
		testPolicy("gpu-cool", 5, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{3, 5}}, coolGPU()),
		testPolicy("ghost", 6, mokkav1alpha1.PolicyTargetRef{RackGroups: []string{"ghost"}}, hotGPU()),
	}
	want := mustEvaluate(t, "dev", testInventory(), testProfiles(), policies)

	reversed := slices.Clone(policies)
	slices.Reverse(reversed)
	rotated := append(slices.Clone(policies[2:]), policies[:2]...)
	for _, order := range [][]*mokkav1alpha1.SGPURuntimePolicy{reversed, rotated} {
		require.Equal(t, want, mustEvaluate(t, "dev", testInventory(), testProfiles(), order))
	}
}

func TestEvaluateDoesNotModifyItsInputs(t *testing.T) {
	t.Parallel()

	inventory := testInventory()
	profiles := testProfiles()
	policies := []*mokkav1alpha1.SGPURuntimePolicy{
		testPolicy("unsorted", 1, mokkav1alpha1.PolicyTargetRef{
			RackGroups:  []string{"training", "inference"},
			RackIndexes: []int32{1, 0},
			GPUIndexes:  []int32{3, 0, 2},
		}, hotGPU()),
		testPolicy("challenger", 2, mokkav1alpha1.PolicyTargetRef{GPUIndexes: []int32{2}}, hotGPU()),
	}
	wantInventory := inventory.DeepCopy()
	wantProfiles := map[string]*mokkav1alpha1.SGPURackProfile{}
	for name, profile := range profiles {
		wantProfiles[name] = profile.DeepCopy()
	}
	wantPolicies := []*mokkav1alpha1.SGPURuntimePolicy{policies[0].DeepCopy(), policies[1].DeepCopy()}

	evaluation := mustEvaluate(t, "dev", inventory, profiles, policies)
	mustRuntime(t, evaluation, testDefaults(), Coordinate{RackGroup: "training", GPUIndex: 2})

	require.Equal(t, wantInventory, inventory)
	require.Equal(t, wantProfiles, profiles)
	require.Equal(t, wantPolicies, policies)
}

// testInventory declares three differently shaped rack groups: training has 4
// racks of 8 Nodes with 4 GPUs, inference has 2 racks of 2 Nodes with 8 GPUs,
// and pending has 3 racks whose profile is missing from the cache.
func testInventory() *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{Name: "dev", UID: "dev-uid"},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{
			{ID: "training", Count: 4, ProfileRef: mokkav1alpha1.ProfileReference{Name: "large"}},
			{ID: "inference", Count: 2, ProfileRef: mokkav1alpha1.ProfileReference{Name: "small"}},
			{ID: "pending", Count: 3, ProfileRef: mokkav1alpha1.ProfileReference{Name: "missing"}},
		}},
	}
}

func testProfiles() map[string]*mokkav1alpha1.SGPURackProfile {
	return map[string]*mokkav1alpha1.SGPURackProfile{
		"large": testProfile("large", 8, 4),
		"small": testProfile("small", 2, 8),
	}
}

func testProfile(name string, nodesPerRack, gpus int32) *mokkav1alpha1.SGPURackProfile {
	return &mokkav1alpha1.SGPURackProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: mokkav1alpha1.SGPURackProfileSpec{
			Rack: mokkav1alpha1.SGPURackShape{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUNode{GPUs: mokkav1alpha1.SGPUGPUs{Count: gpus}},
		},
	}
}

// testPolicy targets inventory dev. It is created at the given second and its
// UID follows its name unless a test overrides it.
func testPolicy(
	name string,
	created int64,
	target mokkav1alpha1.PolicyTargetRef,
	runtime *mokkav1alpha1.RuntimeState,
) *mokkav1alpha1.SGPURuntimePolicy {
	target.Group, target.Kind, target.Name = mokkav1alpha1.GroupName, "SGPUInventory", "dev"
	return &mokkav1alpha1.SGPURuntimePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			UID:               types.UID(name + "-uid"),
			CreationTimestamp: metav1.NewTime(time.Unix(created, 0)),
		},
		Spec: mokkav1alpha1.SGPURuntimePolicySpec{TargetRef: target, Runtime: runtime},
	}
}

func testDefaults() *mokkav1alpha1.RuntimeState {
	return &mokkav1alpha1.RuntimeState{
		DeviceState: mokkav1alpha1.DeviceStateHealthy,
		Telemetry: &mokkav1alpha1.RuntimeTelemetry{
			Power:       &mokkav1alpha1.PowerTelemetry{DrawMilliWatts: ptr.To[int64](175000)},
			Temperature: &mokkav1alpha1.TemperatureTelemetry{Mode: "Fixed", GPUCelsius: ptr.To[int32](38)},
		},
	}
}

func gpuCelsius(celsius int32) *mokkav1alpha1.RuntimeState {
	return &mokkav1alpha1.RuntimeState{Telemetry: &mokkav1alpha1.RuntimeTelemetry{
		Temperature: &mokkav1alpha1.TemperatureTelemetry{GPUCelsius: ptr.To(celsius)},
	}}
}

func hotGPU() *mokkav1alpha1.RuntimeState  { return gpuCelsius(90) }
func coolGPU() *mokkav1alpha1.RuntimeState { return gpuCelsius(40) }

func failedGPU() *mokkav1alpha1.RuntimeState {
	return &mokkav1alpha1.RuntimeState{DeviceState: mokkav1alpha1.DeviceStateFailed}
}

func mustEvaluate(
	t *testing.T,
	inventoryName string,
	inventory *mokkav1alpha1.SGPUInventory,
	profiles map[string]*mokkav1alpha1.SGPURackProfile,
	policies []*mokkav1alpha1.SGPURuntimePolicy,
) *Evaluation {
	t.Helper()

	evaluation, err := Evaluate(inventoryName, inventory, profiles, policies)
	require.NoError(t, err)

	return evaluation
}

func mustRuntime(
	t *testing.T,
	evaluation *Evaluation,
	defaults *mokkav1alpha1.RuntimeState,
	at Coordinate,
) mokkav1alpha1.RuntimeState {
	t.Helper()

	state, err := evaluation.Runtime(defaults, at)
	require.NoError(t, err)

	return state
}

func outcomes(evaluation *Evaluation) map[string]Outcome {
	result := make(map[string]Outcome, len(evaluation.Decisions))
	for _, decision := range evaluation.Decisions {
		result[decision.Policy.Name] = decision.Outcome
	}
	return result
}
