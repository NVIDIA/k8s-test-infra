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

// Layout tests for the memory repair structs the bridge writes into. The C side
// is pinned by _Static_assert in nvml_types.h; this pins go-nvml's side, which
// the engine fills the SRAM entries through.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestEccRepairStructLayouts(t *testing.T) {
	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"RepairStatus", unsafe.Sizeof(nvml.RepairStatus{}), 12},
		{"UnrepairableMemoryStatus_v1", unsafe.Sizeof(nvml.UnrepairableMemoryStatus_v1{}), 4},
		{"DramEncryptionInfo", unsafe.Sizeof(nvml.DramEncryptionInfo{}), 8},
		{"EccSramUniqueUncorrectedErrorEntry_v1", unsafe.Sizeof(nvml.EccSramUniqueUncorrectedErrorEntry_v1{}), 28},
		{"EccSramUniqueUncorrectedErrorCounts", unsafe.Sizeof(nvml.EccSramUniqueUncorrectedErrorCounts{}), 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.goSize,
				"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either the go-nvml version or nvml_types.h",
				tc.name, tc.goSize, tc.expected)
		})
	}
}

// TestEccRepairVersions_MatchGoNvml pins the tags the bridge demands against the
// ones go-nvml's STRUCT_VERSION helper produces, so a caller built on go-nvml
// never meets ARGUMENT_VERSION_MISMATCH. Calling the production functions puts
// the C struct sizes on one side of the comparison.
func TestEccRepairVersions_MatchGoNvml(t *testing.T) {
	require.Equal(t, nvml.STRUCT_VERSION(nvml.RepairStatus{}, 1), repairStatusVersion())
	require.Equal(t, nvml.STRUCT_VERSION(nvml.DramEncryptionInfo{}, 1), dramEncryptionVersion())
	require.Equal(t, nvml.STRUCT_VERSION(nvml.EccSramUniqueUncorrectedErrorCounts{}, 1), sramUniqueCountsVersion())
}
