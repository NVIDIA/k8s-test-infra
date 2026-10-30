// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout tests for the Confidential Compute structs the bridge reads and
// writes. See fabric_layout_test.go for why the expected sizes are constants
// rather than C.sizeof_* reads; nvml_types.h pins the same sizes with
// _Static_assert, so the two sides cannot drift apart silently.
//
// The key rotation structs are the ones to watch: their unsigned long long sits
// behind one uint, so a missing pad would read the advantage from the wrong
// eight bytes.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Expected byte sizes derived from nvml_types.h field-by-field.
//
//	ConfComputeSystemCaps:          cpuCaps(4) + gpusCaps(4) = 8
//	ConfComputeSystemState:         environment(4) + ccFeature(4) + devToolsMode(4) = 12
//	SystemConfComputeSettings:      version(4) + four uints(16) = 20
//	ConfComputeGpuCertificate:      two sizes(8) + certChain(4096) + attestationCertChain(5120) = 9224
//	ConfComputeGpuAttestationReport: three uints(12) + nonce(32) + report(8192) + cecReport(4096) = 12332
//	ConfComputeKeyRotationThreshold: version(4) + pad(4) + advantage(8) = 16
func TestConfComputeStructLayouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"ConfComputeSystemCaps", unsafe.Sizeof(nvml.ConfComputeSystemCaps{}), 8},
		{"ConfComputeSystemState", unsafe.Sizeof(nvml.ConfComputeSystemState{}), 12},
		{"SystemConfComputeSettings", unsafe.Sizeof(nvml.SystemConfComputeSettings{}), 20},
		{"ConfComputeGpuCertificate", unsafe.Sizeof(nvml.ConfComputeGpuCertificate{}), 9224},
		{"ConfComputeGpuAttestationReport", unsafe.Sizeof(nvml.ConfComputeGpuAttestationReport{}), 12332},
		{"ConfComputeGetKeyRotationThresholdInfo", unsafe.Sizeof(nvml.ConfComputeGetKeyRotationThresholdInfo{}), 16},
		{"ConfComputeSetKeyRotationThresholdInfo", unsafe.Sizeof(nvml.ConfComputeSetKeyRotationThresholdInfo{}), 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, tc.goSize,
				"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either the go-nvml version or nvml_types.h",
				tc.name, tc.goSize, tc.expected)
		})
	}
}

// TestConfComputeStructVersions_MatchCallers pins the version tags the bridge
// accepts against the ones go-nvml stamps. A disagreement would turn every
// call through go-nvml into ARGUMENT_VERSION_MISMATCH.
func TestConfComputeStructVersions_MatchCallers(t *testing.T) {
	t.Parallel()
	require.Equal(t, nvml.STRUCT_VERSION(nvml.SystemConfComputeSettings{}, 1), confComputeSettingsVersion())
	require.Equal(t, uint32(0x1000014), confComputeSettingsVersion())
	require.Equal(t, nvml.STRUCT_VERSION(nvml.ConfComputeGetKeyRotationThresholdInfo{}, 1),
		confComputeGetKeyRotationVersion())
	require.Equal(t, nvml.STRUCT_VERSION(nvml.ConfComputeSetKeyRotationThresholdInfo{}, 1),
		confComputeSetKeyRotationVersion())
	require.Equal(t, uint32(0x1000010), confComputeSetKeyRotationVersion())
}

// An old or careless caller that leaves the version unset, or stamps a future
// one, must be refused rather than read with the wrong layout.
func TestConfComputeStructVersionOK_RejectsOtherVersions(t *testing.T) {
	t.Parallel()
	v1 := confComputeSettingsVersion()
	require.True(t, confComputeStructVersionOK("nvmlSystemGetConfComputeSettings", v1, v1))
	require.False(t, confComputeStructVersionOK("nvmlSystemGetConfComputeSettings", 0, v1))
	require.False(t, confComputeStructVersionOK("nvmlSystemGetConfComputeSettings", FabricStructVersion(20, 2), v1))
}
