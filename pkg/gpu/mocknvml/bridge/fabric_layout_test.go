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

// Layout tests for the fabric structs the bridge writes into.
//
// These pin the sizes of the vendored go-nvml fabric structs against
// constants that document the hand-written C layouts in nvml_types.h.
// If go-nvml ever bumps a struct in a way that diverges from the C
// layout, the bridge would silently write the wrong bytes into a
// caller's buffer — catching that drift here is far cheaper than in
// downstream integration.
//
// Note on test approach: Go forbids cgo in test files for packages
// that contain //export directives, so we cannot read C.sizeof_*
// directly from the test. Instead, we hard-code the expected sizes
// (derived from the field-by-field layout in nvml_types.h, accounting
// for natural alignment) as constants. Any change to the C structs
// must be accompanied by the matching constant update below; any
// change to the go-nvml structs without a matching C change will trip
// these tests.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Expected byte sizes derived from nvml_types.h field-by-field. Update
// in lockstep with any C struct change.
//
//	v1: uuid[16] + status(4) + cliqueId(4) + state(1) + 3 trailing pad = 28
//	v2: version(4) + uuid[16] + status(4) + cliqueId(4) + state(1)
//	    + 3 inner pad + healthMask(4) = 36
//	v3: v2 layout + healthSummary(1) + 3 trailing pad = 40
const (
	expectedFabricInfoSize   uintptr = 28
	expectedFabricInfoV2Size uintptr = 36
	expectedFabricInfoV3Size uintptr = 40
)

func TestFabricStructLayouts(t *testing.T) {
	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"GpuFabricInfo (v1)", unsafe.Sizeof(nvml.GpuFabricInfo{}), expectedFabricInfoSize},
		{"GpuFabricInfo_v2", unsafe.Sizeof(nvml.GpuFabricInfo_v2{}), expectedFabricInfoV2Size},
		{"GpuFabricInfo_v3", unsafe.Sizeof(nvml.GpuFabricInfo_v3{}), expectedFabricInfoV3Size},
		// V is the typedef alias for v3 in nvml_types.h.
		{"GpuFabricInfoV (v3 alias)", unsafe.Sizeof(nvml.GpuFabricInfoV{}), expectedFabricInfoV3Size},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.goSize,
				"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either go-nvml vendor or nvml_types.h",
				tc.name, tc.goSize, tc.expected)
		})
	}
}

// TestFabricStructVersion_MatchesGoNvml asserts the encoding the bridge
// uses to tag versioned NVML structs matches go-nvml's STRUCT_VERSION
// helper. Real consumers go through go-nvml's GpuFabricInfoHandler,
// which sets `info.Version = STRUCT_VERSION(info, N)` before calling
// into the bridge. If our encoder ever diverges from go-nvml's, every
// real caller's version tag mismatches our switch and the dispatch
// path silently changes — caught here.
func TestFabricStructVersion_MatchesGoNvml(t *testing.T) {
	v2 := nvml.GpuFabricInfo_v2{}
	v3 := nvml.GpuFabricInfo_v3{}
	cases := []struct {
		name   string
		ours   uint32
		theirs uint32
	}{
		{
			name:   "v2",
			ours:   FabricStructVersion(unsafe.Sizeof(v2), 2),
			theirs: nvml.STRUCT_VERSION(v2, 2),
		},
		{
			name:   "v3",
			ours:   FabricStructVersion(unsafe.Sizeof(v3), 3),
			theirs: nvml.STRUCT_VERSION(v3, 3),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.theirs, tc.ours,
				"%s: bridge encoded 0x%x, go-nvml encoded 0x%x — version-tag mismatch",
				tc.name, tc.ours, tc.theirs)
		})
	}
}
