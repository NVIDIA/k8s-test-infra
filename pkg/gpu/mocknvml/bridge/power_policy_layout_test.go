// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout tests for the PowerMizer and power smoothing structs the bridge reads
// and writes. See fabric_layout_test.go for why the expected sizes are
// constants rather than C.sizeof_* reads.
//
// nvmlPowerSmoothingProfile_t is the one to watch: its double sits behind three
// uints, so a missing pad would read the ramp rate from the wrong four bytes
// and every -rur / -rdr / -ptf would store a garbage value.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Expected byte sizes derived from nvml_types.h field-by-field. Update in
// lockstep with any C struct change.
//
//	DevicePowerMizerModes_v1: currentMode(4) + mode(4) + supportedPowerMizerModes(4) = 12
//	PowerSmoothingProfile:    version(4) + profileId(4) + paramId(4) + pad(4) + value(8) = 24
//	PowerSmoothingState:      version(4) + state(4) = 8
const (
	expectedPowerMizerModesSize       uintptr = 12
	expectedPowerSmoothingProfileSize uintptr = 24
	expectedPowerSmoothingStateSize   uintptr = 8
)

func TestPowerPolicyStructLayouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"DevicePowerMizerModes_v1", unsafe.Sizeof(nvml.DevicePowerMizerModes_v1{}), expectedPowerMizerModesSize},
		{"PowerSmoothingProfile", unsafe.Sizeof(nvml.PowerSmoothingProfile{}), expectedPowerSmoothingProfileSize},
		{"PowerSmoothingState", unsafe.Sizeof(nvml.PowerSmoothingState{}), expectedPowerSmoothingStateSize},
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

// TestPowerSmoothingProfileVersion_MatchesCallers pins the tag
// UpdatePresetProfileParam demands against the ones its callers stamp: go-nvml's,
// and the 0x1000018 nvidia-smi 580 was observed passing. A tag that disagrees
// turns every `nvidia-smi power-smoothing -ptf/-rur/-rdr/-rdh` into
// ARGUMENT_VERSION_MISMATCH.
func TestPowerSmoothingProfileVersion_MatchesCallers(t *testing.T) {
	t.Parallel()
	require.Equal(t, nvml.STRUCT_VERSION(nvml.PowerSmoothingProfile{}, 1), powerSmoothingProfileVersion())
	require.Equal(t, uint32(0x1000018), powerSmoothingProfileVersion())
}
