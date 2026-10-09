// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout tests for the clock structs the bridge reads and writes. See
// fabric_layout_test.go for why the expected sizes are constants rather than
// C.sizeof_* reads.

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
//	ClockOffset: version, type, pstate, clockOffsetMHz, min, max = 6 * 4 = 24
//	PerfModes / CurrentClockFreqs: version(4) + str[2048] = 2052
//	GpuDynamicPstatesInfo: flags(4) + 8 utilizations * 4 fields * 4 = 132
const (
	expectedClockOffsetSize           uintptr = 24
	expectedPerfModesStringSize       uintptr = 2052
	expectedDynamicPstatesInfoSize    uintptr = 132
	expectedDynamicPstatesDomainCount         = 8
)

func TestClockStructLayouts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"ClockOffset", unsafe.Sizeof(nvml.ClockOffset{}), expectedClockOffsetSize},
		{"DevicePerfModes", unsafe.Sizeof(nvml.DevicePerfModes{}), expectedPerfModesStringSize},
		{"DeviceCurrentClockFreqs", unsafe.Sizeof(nvml.DeviceCurrentClockFreqs{}), expectedPerfModesStringSize},
		{"GpuDynamicPstatesInfo", unsafe.Sizeof(nvml.GpuDynamicPstatesInfo{}), expectedDynamicPstatesInfoSize},
	}
	for _, tc := range cases {
		require.Equal(t, tc.expected, tc.goSize,
			"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either the go-nvml version or nvml_types.h",
			tc.name, tc.goSize, tc.expected)
	}
	require.Len(t, nvml.GpuDynamicPstatesInfo{}.Utilization, expectedDynamicPstatesDomainCount,
		"the bridge copies every engine domain into the C array and would overrun a shorter one")
}

// TestClockStructVersions_MatchGoNvml pins the tags the bridge demands against
// the ones a caller stamping with go-nvml's STRUCT_VERSION produces. The
// bridge derives its tags from the C struct sizes, so this also catches a C
// struct that drifted from go-nvml's.
func TestClockStructVersions_MatchGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, nvml.STRUCT_VERSION(nvml.ClockOffset{}, 1), clockOffsetVersion())
	require.Equal(t, nvml.STRUCT_VERSION(nvml.DevicePerfModes{}, 1), perfModesVersion())
	require.Equal(t, nvml.STRUCT_VERSION(nvml.DeviceCurrentClockFreqs{}, 1), currentClockFreqsVersion())
}
