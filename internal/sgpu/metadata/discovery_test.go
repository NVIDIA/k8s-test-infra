// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"encoding/json"
	"os"
	"testing"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"github.com/stretchr/testify/require"
)

// The fixtures are one-shot GFD v0.20.2 output from Mokka's CPU NVML,
// PCI and machine-type surfaces, with MIG profiles loaded and timestamps off.
func TestDiscoveryLabelsMatchCPUProfiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, product, family, compute, memory string
		count                                  int
		mig                                    bool
	}{
		{"a100", "NVIDIA A100-SXM4-40GB", "Ampere", "8.0", "40Gi", 8, true},
		{"t4", "NVIDIA T4", "Turing", "7.5", "16Gi", 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/gfd-" + tc.name + ".json")
			require.NoError(t, err)
			want := map[string]string{}
			require.NoError(t, json.Unmarshal(raw, &want))
			want[NvidiaPCILabel] = "true"
			want[MIGStrategyLabel] = "none"
			hw := state.Hardware{Software: &api.SGPUSoftware{DriverVersion: "580.65.06", CUDAVersion: "13.0"}, GPUModel: state.GPUModel{Vendor: "NVIDIA", ProductName: tc.product, Architecture: tc.family, ComputeCapability: tc.compute, Memory: tc.memory, MIGCapable: tc.mig}}
			got, err := DiscoveryLabels(tc.count, hw)
			require.NoError(t, err)
			require.Equal(t, want, got)
			for key := range got {
				require.Contains(t, DiscoveryLabelKeys(), key)
			}
		})
	}
}

func TestDiscoveryLabelsWithMissingInputs(t *testing.T) {
	t.Parallel()
	hw := state.Hardware{GPUModel: state.GPUModel{Memory: "40Gi"}}
	hw.GPUModel.PCI.VendorID = "1234"
	got, err := DiscoveryLabels(1, hw)
	require.NoError(t, err)
	for _, key := range []string{GPUProductLabel, NvidiaPCILabel, "nvidia.com/cuda.driver-version.full", "nvidia.com/gpu.compute.major", "nvidia.com/gpu.machine"} {
		require.NotContains(t, got, key)
	}
	hw.Software = &api.SGPUSoftware{DriverVersion: "550.90"}
	got, err = DiscoveryLabels(1, hw)
	require.NoError(t, err)
	require.Contains(t, got, "nvidia.com/cuda.driver-version.revision")
	require.Empty(t, got["nvidia.com/cuda.driver-version.revision"])
}

func TestDiscoveryGPUFamily(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ architecture, capability, family string }{
		{"ada_lovelace", "8.9", "ada-lovelace"},
		{"", "8.9", "ada-lovelace"},
		{"unknown", "7.0", "volta"},
		{"unknown", "7.5", "turing"},
		{"", "15.0", "undefined"},
	} {
		hw := state.Hardware{GPUModel: state.GPUModel{Architecture: tc.architecture, ComputeCapability: tc.capability, Memory: "1Gi"}}
		got, err := DiscoveryLabels(1, hw)
		require.NoError(t, err)
		require.Equal(t, tc.family, got["nvidia.com/gpu.family"])
	}
}
