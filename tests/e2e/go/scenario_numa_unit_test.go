//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestSingleZoneNUMAProfilePreservesDeviceIdentity(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../../deployments/nvml-mock/helm/nvml-mock/profiles/a100.yaml")
	require.NoError(t, err)
	converted, err := singleZoneNUMAProfile(string(raw))
	require.NoError(t, err)
	var before, after map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &before))
	require.NoError(t, yaml.Unmarshal([]byte(converted), &after))
	roots := before["pcie_topology"].(map[string]any)["root_complexes"].([]any)
	for _, root := range roots {
		root.(map[string]any)["numa_node"] = float64(0)
	}
	require.Equal(t, before, after, "only the NUMA placement may change; PCI IDs, UUIDs, and all other settings must stay intact")
}

func TestSingleZoneNUMAProfileRejectsInvalidTopology(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"[", "version: '1.0'", "pcie_topology: {}", "pcie_topology: {root_complexes: [null, null]}"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := singleZoneNUMAProfile(raw)
			require.Error(t, err)
		})
	}
}

func TestGPUSwapPreservesCPUAndDeviceConfiguration(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../../deployments/nvml-mock/helm/nvml-mock/profiles/a100.yaml")
	require.NoError(t, err)
	swapped, err := swappedGPUProfile(string(raw))
	require.NoError(t, err)
	var before, after map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &before))
	require.NoError(t, yaml.Unmarshal([]byte(swapped), &after))
	roots := before["pcie_topology"].(map[string]any)["root_complexes"].([]any)
	roots[0].(map[string]any)["numa_node"] = float64(1)
	roots[1].(map[string]any)["numa_node"] = float64(0)
	require.Equal(t, before, after, "only GPU locality may change; CPU affinity, BDFs, UUIDs, counts, and memory settings must stay intact")
	restored, err := swappedGPUProfile(swapped)
	require.NoError(t, err)
	var original, restoredProfile map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &original))
	require.NoError(t, yaml.Unmarshal([]byte(restored), &restoredProfile))
	require.Equal(t, original, restoredProfile)
}
