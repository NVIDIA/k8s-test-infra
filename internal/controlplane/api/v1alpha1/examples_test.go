// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package v1alpha1_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

func TestControllerExamplesAreTypedAndMaterializable(t *testing.T) {
	examples := filepath.Join("..", "..", "..", "..", "examples", "mokka-controller")
	profileData, err := os.ReadFile(filepath.Join(examples, "sgpu-rack-profile.yaml"))
	require.NoError(t, err)
	profile := &mokkav1alpha1.SGPURackProfile{}
	require.NoError(t, yaml.UnmarshalStrict(profileData, profile))
	require.Equal(t, mokkav1alpha1.SchemeGroupVersion.String(), profile.APIVersion)
	require.Equal(t, "SGPURackProfile", profile.Kind)
	require.NoError(t, materialize.ValidateProfile(profile.Spec))
	require.Positive(t, profile.Spec.Node.GPUs.Memory.Capacity.Value())
	require.Positive(t, profile.Spec.Node.GPUs.Clocks.Supported[0].MemoryMHz)

	inventoryData, err := os.ReadFile(filepath.Join(examples, "sgpu-inventory.yaml"))
	require.NoError(t, err)
	inventory := &mokkav1alpha1.SGPUInventory{}
	require.NoError(t, yaml.UnmarshalStrict(inventoryData, inventory))
	require.Equal(t, mokkav1alpha1.SchemeGroupVersion.String(), inventory.APIVersion)
	require.Equal(t, "SGPUInventory", inventory.Kind)
	require.Len(t, inventory.Spec.RackGroups, 1)
	selector, err := metav1.LabelSelectorAsSelector(inventory.Spec.RackGroups[0].Placement.NodeSelector)
	require.NoError(t, err)
	require.Equal(t, "mokka.nvidia.com/pool=example", selector.String())
}
