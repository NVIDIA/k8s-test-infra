// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpupolicy "github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackrender"
)

func TestControllerExamplesAreTypedAndMaterializable(t *testing.T) {
	profile := &mokkav1alpha1.SGPURackProfile{}
	loadExample(t, "sgpu-rack-profile.yaml", profile)
	require.Equal(t, mokkav1alpha1.SchemeGroupVersion.String(), profile.APIVersion)
	require.Equal(t, "SGPURackProfile", profile.Kind)
	require.NoError(t, rackrender.ValidateProfile(profile.Spec))
	require.Positive(t, profile.Spec.Node.GPUs.Memory.Capacity.Value())
	require.NotEmpty(t, profile.Spec.Node.GPUs.Clocks.Supported)
	for _, clocks := range profile.Spec.Node.GPUs.Clocks.Supported {
		require.Positive(t, clocks.MemoryMHz)
	}

	inventory := &mokkav1alpha1.SGPUInventory{}
	loadExample(t, "sgpu-inventory.yaml", inventory)
	require.Equal(t, mokkav1alpha1.SchemeGroupVersion.String(), inventory.APIVersion)
	require.Equal(t, "SGPUInventory", inventory.Kind)
	require.Len(t, inventory.Spec.RackGroups, 1)
	selector, err := metav1.LabelSelectorAsSelector(inventory.Spec.RackGroups[0].Placement.NodeSelector)
	require.NoError(t, err)
	require.Equal(t, "mokka.nvidia.com/pool=example", selector.String())
}

func TestRuntimePolicyExampleIsAcceptedForTheExampleInventory(t *testing.T) {
	t.Parallel()

	profile := &mokkav1alpha1.SGPURackProfile{}
	loadExample(t, "sgpu-rack-profile.yaml", profile)
	inventory := &mokkav1alpha1.SGPUInventory{}
	loadExample(t, "sgpu-inventory.yaml", inventory)
	policy := &mokkav1alpha1.SGPURuntimePolicy{}
	loadExample(t, "sgpu-runtime-policy.yaml", policy)
	require.Equal(t, mokkav1alpha1.SchemeGroupVersion.String(), policy.APIVersion)
	require.Equal(t, "SGPURuntimePolicy", policy.Kind)

	evaluation := sgpupolicy.Evaluate(inventory.Name, inventory,
		map[string]*mokkav1alpha1.SGPURackProfile{profile.Name: profile},
		[]*mokkav1alpha1.SGPURuntimePolicy{policy})

	require.Len(t, evaluation.Decisions, 1)
	require.Equal(t, sgpupolicy.Accepted, evaluation.Decisions[0].Outcome, evaluation.Decisions[0].Message)
}

func loadExample(t *testing.T, name string, object any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "controlplane-crds", name))
	require.NoError(t, err)
	require.NoError(t, yaml.UnmarshalStrict(data, object))
}
