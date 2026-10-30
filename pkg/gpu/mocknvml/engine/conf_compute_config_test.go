// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

func TestResolveConfCompute_DefaultsOff(t *testing.T) {
	t.Parallel()

	hopper := resolveConfCompute(&YAMLConfig{}, []gpuarch.Arch{gpuarch.Hopper, gpuarch.Hopper})
	require.True(t, hopper.Capable)
	require.False(t, hopper.On)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_PROD, hopper.Environment)
	require.EqualValues(t, 60, hopper.AttackerAdvantage)

	ampere := resolveConfCompute(&YAMLConfig{}, []gpuarch.Arch{gpuarch.Ampere})
	require.False(t, ampere.Capable)
	require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_UNAVAILABLE, ampere.Environment)

	mixed := resolveConfCompute(&YAMLConfig{}, []gpuarch.Arch{gpuarch.Hopper, gpuarch.Ampere})
	require.False(t, mixed.Capable, "one old GPU makes the node not capable")
}

func TestResolveConfCompute_MapsEveryEnum(t *testing.T) {
	t.Parallel()

	archs := []gpuarch.Arch{gpuarch.Blackwell}
	tests := []struct {
		name  string
		block ConfidentialComputeConfig
		check func(t *testing.T, s ConfComputeSpec)
	}{
		{"mode on", ConfidentialComputeConfig{Mode: "on"}, func(t *testing.T, s ConfComputeSpec) {
			require.True(t, s.On)
			require.False(t, s.DevTools)
		}},
		{"mode devtools", ConfidentialComputeConfig{Mode: "devtools"}, func(t *testing.T, s ConfComputeSpec) {
			require.True(t, s.On)
			require.True(t, s.DevTools)
		}},
		{"environment sim", ConfidentialComputeConfig{Environment: "sim"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_ENVIRONMENT_SIM, s.Environment)
		}},
		{"protected_pcie", ConfidentialComputeConfig{MultiGPUMode: "protected_pcie"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_MULTIGPU_PROTECTED_PCIE, s.MultiGPUMode)
		}},
		{"nvle", ConfidentialComputeConfig{MultiGPUMode: "nvle"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_MULTIGPU_NVLE, s.MultiGPUMode)
		}},
		{"amd_sev", ConfidentialComputeConfig{CPUCaps: "amd_sev"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV, s.CPUCaps)
		}},
		{"intel_tdx", ConfidentialComputeConfig{CPUCaps: "intel_tdx"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_CPU_CAPS_INTEL_TDX, s.CPUCaps)
		}},
		{"amd_sev_snp", ConfidentialComputeConfig{CPUCaps: "amd_sev_snp"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV_SNP, s.CPUCaps)
		}},
		{"amd_snp_vtom", ConfidentialComputeConfig{CPUCaps: "amd_snp_vtom"}, func(t *testing.T, s ConfComputeSpec) {
			require.EqualValues(t, nvml.CC_SYSTEM_CPU_CAPS_AMD_SNP_VTOM, s.CPUCaps)
		}},
		{"scalars", ConfidentialComputeConfig{Ready: true, KeyRotationAttackerAdvantage: 55, UnprotectedMemKiB: 4096}, func(t *testing.T, s ConfComputeSpec) {
			require.True(t, s.Ready)
			require.EqualValues(t, 55, s.AttackerAdvantage)
			require.EqualValues(t, 4096, s.UnprotectedMemKiB)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t, resolveConfCompute(&YAMLConfig{ConfidentialCompute: &tt.block}, archs))
		})
	}
}

func ccProfile(arch string, block ConfidentialComputeConfig) *YAMLConfig {
	return &YAMLConfig{
		DeviceDefaults: DeviceConfig{
			Architecture: arch,
			Memory:       &MemoryConfig{TotalBytes: 80 << 30},
		},
		ConfidentialCompute: &block,
	}
}

func TestValidateConfCompute_Refuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *YAMLConfig
		key  string
	}{
		{"unknown mode", ccProfile("hopper", ConfidentialComputeConfig{Mode: "maybe"}), "mode"},
		{"unknown environment", ccProfile("hopper", ConfidentialComputeConfig{Environment: "x"}), "environment"},
		{"unknown multi_gpu_mode", ccProfile("hopper", ConfidentialComputeConfig{MultiGPUMode: "x"}), "multi_gpu_mode"},
		{"unknown cpu_caps", ccProfile("hopper", ConfidentialComputeConfig{CPUCaps: "x"}), "cpu_caps"},
		{"advantage 49", ccProfile("hopper", ConfidentialComputeConfig{KeyRotationAttackerAdvantage: 49}), "key_rotation_attacker_advantage"},
		{"advantage 66", ccProfile("hopper", ConfidentialComputeConfig{KeyRotationAttackerAdvantage: 66}), "key_rotation_attacker_advantage"},
		{"on with A100", ccProfile("ampere", ConfidentialComputeConfig{Mode: "on"}), "mode"},
		{"nvle on H100", ccProfile("hopper", ConfidentialComputeConfig{Mode: "on", MultiGPUMode: "nvle"}), "multi_gpu_mode"},
		{"protected_pcie with CC on", ccProfile("hopper", ConfidentialComputeConfig{Mode: "on", MultiGPUMode: "protected_pcie"}), "multi_gpu_mode"},
		{"protected_pcie with devtools", ccProfile("hopper", ConfidentialComputeConfig{Mode: "devtools", MultiGPUMode: "protected_pcie"}), "multi_gpu_mode"},
		{"protected_pcie on Blackwell", ccProfile("blackwell", ConfidentialComputeConfig{MultiGPUMode: "protected_pcie"}), "multi_gpu_mode"},
		{"protected_pcie on Rubin", ccProfile("rubin", ConfidentialComputeConfig{MultiGPUMode: "protected_pcie"}), "multi_gpu_mode"},
		{"protected_pcie on A100", ccProfile("ampere", ConfidentialComputeConfig{MultiGPUMode: "protected_pcie"}), "multi_gpu_mode"},
		{"unprotected beyond framebuffer", ccProfile("hopper", ConfidentialComputeConfig{UnprotectedMemKiB: (80 << 20) + 1}), "unprotected_mem_kib"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConfCompute(tt.cfg)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.key)
		})
	}
}

func TestValidateConfCompute_ChecksPerDeviceOverrides(t *testing.T) {
	t.Parallel()

	cfg := ccProfile("hopper", ConfidentialComputeConfig{Mode: "on"})
	cfg.Devices = []DeviceOverride{{Index: 0}, {Index: 1, Architecture: "ampere"}}
	err := validateConfCompute(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mode")
}

func TestValidateConfCompute_AcceptsValid(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateConfCompute(&YAMLConfig{}))
	require.NoError(t, validateConfCompute(ccProfile("hopper", ConfidentialComputeConfig{
		Mode: "off", MultiGPUMode: "protected_pcie",
	})))
	require.NoError(t, validateConfCompute(ccProfile("blackwell", ConfidentialComputeConfig{
		Mode: "off", MultiGPUMode: "nvle",
	})))
	require.NoError(t, validateConfCompute(ccProfile("blackwell", ConfidentialComputeConfig{
		Mode: "devtools", MultiGPUMode: "nvle", KeyRotationAttackerAdvantage: 65, UnprotectedMemKiB: 80 << 20,
	})))
}

func TestValidateConfCompute_AcceptsShippedProfiles(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("../configs/*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		// A .mig.yaml is a MIG profile table overlay, not a standalone profile.
		if strings.HasSuffix(f, ".mig.yaml") {
			continue
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			cfg, err := LoadYAMLConfig(f)
			require.NoError(t, err)
			require.NoError(t, validateConfCompute(cfg))
		})
	}
}

// TestValidateConfCompute_ChecksEveryCreatedDevice covers a GPU that falls
// back to the defaults without the override list being shorter than the
// count: overrides at 0 and 2 with num_devices 2 still create GPU 1 from the
// Ampere defaults, which must refuse CC.
func TestValidateConfCompute_ChecksEveryCreatedDevice(t *testing.T) {
	t.Parallel()

	cfg := ccProfile("ampere", ConfidentialComputeConfig{Mode: "on"})
	cfg.System.NumDevices = 2
	cfg.Devices = []DeviceOverride{{Index: 0, Architecture: "hopper"}, {Index: 2, Architecture: "hopper"}}
	err := validateConfCompute(cfg)
	require.Error(t, err, "GPU 1 is an Ampere board from the defaults")
	require.Contains(t, err.Error(), "mode")
}
