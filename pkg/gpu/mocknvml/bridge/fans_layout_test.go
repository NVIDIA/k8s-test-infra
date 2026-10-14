// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout tests for the fan and cooling structs the bridge writes into. See
// fabric_layout_test.go for why the expected sizes are constants rather than
// C.sizeof_* reads.
//
// These three structs carry an unusual risk. In nvmlFanSpeedInfo_t and
// nvmlCoolerInfo_t the caller writes an index into the struct and the bridge
// reads it back out, so drift does not merely truncate an output: the bridge
// would take the version word for a fan index, ask about a fan the board does
// not have, and answer INVALID_ARGUMENT for every query. nvmlUnitFanSpeeds_t is
// 196 bytes behind a fixed 24-element array, where a wrong element stride walks
// off the end of the caller's buffer.

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
//	FanSpeedInfo:  version(4) + fan(4) + speed(4) = 12
//	CoolerInfo:    version(4) + index(4) + signalType(4) + target(4) = 16
//	UnitFanInfo:   speed(4) + state(4) = 8
//	UnitFanSpeeds: 24 * 8 + count(4) = 196
const (
	expectedFanSpeedInfoSize  uintptr = 12
	expectedCoolerInfoSize    uintptr = 16
	expectedUnitFanInfoSize   uintptr = 8
	expectedUnitFanSpeedsSize uintptr = 196
)

func TestFanStructLayouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"FanSpeedInfo", unsafe.Sizeof(nvml.FanSpeedInfo{}), expectedFanSpeedInfoSize},
		{"CoolerInfo", unsafe.Sizeof(nvml.CoolerInfo{}), expectedCoolerInfoSize},
		{"UnitFanInfo", unsafe.Sizeof(nvml.UnitFanInfo{}), expectedUnitFanInfoSize},
		{"UnitFanSpeeds", unsafe.Sizeof(nvml.UnitFanSpeeds{}), expectedUnitFanSpeedsSize},
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

// TestFanStructVersions_MatchGoNvml pins the version tags the two getters demand
// against the ones a go-nvml caller stamps. Both structs are written by the
// bridge, so the tag is what keeps the write inside the caller's allocation —
// and a tag that disagrees turns every RPM and cooler query into
// ARGUMENT_VERSION_MISMATCH, which reads as a board that has no fan telemetry
// rather than as a mismatch.
//
// Calling the production functions is what makes this a real pin: they read
// sizeof of the C structs, so the C layout sits on one side of each comparison
// and go-nvml's on the other.
func TestFanStructVersions_MatchGoNvml(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		ours   uint32
		theirs uint32
	}{
		{"FanSpeedInfo", fanSpeedInfoVersion(), nvml.STRUCT_VERSION(nvml.FanSpeedInfo{}, 1)},
		{"CoolerInfo", coolerInfoVersion(), nvml.STRUCT_VERSION(nvml.CoolerInfo{}, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.theirs, tc.ours,
				"bridge demands 0x%x, a go-nvml caller stamps 0x%x — every %s query would answer "+
					"ARGUMENT_VERSION_MISMATCH; reconcile nvml_types.h with the go-nvml struct",
				tc.ours, tc.theirs, tc.name)
		})
	}
}
