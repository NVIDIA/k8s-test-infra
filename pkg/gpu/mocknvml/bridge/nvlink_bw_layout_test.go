// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Layout tests for the NVLink bandwidth-mode structs the bridge writes into.
//
// Go forbids cgo in test files for a package that contains //export
// directives, so these cannot read C.sizeof_* directly. The expected sizes are
// instead derived field-by-field from nvml_types.h (accounting for natural
// alignment) and checked against the go-nvml structs. This guards the go-nvml
// side only: it catches an upstream bump that changes a layout out from under
// us, which is far cheaper than discovering the drift as garbage in a caller's
// buffer.
//
// The C side is guarded separately, by _Static_assert on both sizeof and
// offsetof in nvml_types.h. Those live there rather than here precisely
// because this file cannot see the C structs, and offsets are asserted as well
// as sizes because neither a field reorder nor a one-element array change
// alters sizeof once padding is accounted for.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Expected byte sizes derived from nvml_types.h. Update in lockstep with any
// C struct change.
//
//	SupportedBwModes: version(4) + bwModes[23] + totalBwModes(1) = 28
//	GetBwMode:        version(4) + bIsBest(4) + bwMode(1) + 3 pad = 12
//	SetBwMode:        version(4) + bSetBest(4) + bwMode(1) + 3 pad = 12
//	NvLinkInfo_v1:    version(4) + isNvleEnabled(4) = 8
//	NvLinkPowerThres: lowPwrThreshold(4) = 4
//
// The firmware chain behind NvLinkInfo_v2 is pinned too, because v2's size is
// what nvmlDeviceGetNvLinkInfo's accepted version tag is built from — a drift
// there would silently start rejecting real callers:
//
//	NvlinkFirmwareVersion: ucodeType(1) + 3 pad + major/minor/subMinor(12) = 16
//	NvlinkFirmwareInfo:    firmwareVersion[100](1600) + numValidEntries(4) = 1604
//	NvLinkInfo_v2:         version(4) + isNvleEnabled(4) + firmwareInfo(1604) = 1612
const (
	expectedNvlinkSupportedBwModesSize uintptr = 28
	expectedNvlinkGetBwModeSize        uintptr = 12
	expectedNvlinkSetBwModeSize        uintptr = 12
	expectedNvLinkInfoV1Size           uintptr = 8
	expectedNvLinkInfoV2Size           uintptr = 1612
	expectedNvlinkFirmwareVersionSize  uintptr = 16
	expectedNvlinkFirmwareInfoSize     uintptr = 1604
	expectedNvLinkPowerThresSize       uintptr = 4
)

func TestNvlinkBwModeStructLayouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"NvlinkSupportedBwModes", unsafe.Sizeof(nvml.NvlinkSupportedBwModes{}), expectedNvlinkSupportedBwModesSize},
		{"NvlinkGetBwMode", unsafe.Sizeof(nvml.NvlinkGetBwMode{}), expectedNvlinkGetBwModeSize},
		{"NvlinkSetBwMode", unsafe.Sizeof(nvml.NvlinkSetBwMode{}), expectedNvlinkSetBwModeSize},
		{"NvLinkInfo_v1", unsafe.Sizeof(nvml.NvLinkInfo_v1{}), expectedNvLinkInfoV1Size},
		{"NvLinkInfo_v2", unsafe.Sizeof(nvml.NvLinkInfo_v2{}), expectedNvLinkInfoV2Size},
		{"NvlinkFirmwareVersion", unsafe.Sizeof(nvml.NvlinkFirmwareVersion{}), expectedNvlinkFirmwareVersionSize},
		{"NvlinkFirmwareInfo", unsafe.Sizeof(nvml.NvlinkFirmwareInfo{}), expectedNvlinkFirmwareInfoSize},
		{"NvLinkPowerThres", unsafe.Sizeof(nvml.NvLinkPowerThres{}), expectedNvLinkPowerThresSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, tc.goSize,
				"go-nvml %s size diverged from the C layout in nvml_types.h", tc.name)
		})
	}
}

// TestNvlinkSupportedBwModesArrayBound pins the array bound against the
// upstream NVML_NVLINK_TOTAL_SUPPORTED_BW_MODES of 23. The mock only ever
// fills the first few entries, but the struct the caller allocates is sized by
// this constant, so a mismatch would overrun.
func TestNvlinkSupportedBwModesArrayBound(t *testing.T) {
	t.Parallel()

	var modes nvml.NvlinkSupportedBwModes
	require.Len(t, modes.BwModes, 23, "NVML_NVLINK_TOTAL_SUPPORTED_BW_MODES")
}
