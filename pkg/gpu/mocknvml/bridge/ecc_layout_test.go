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

// Layout tests for the ECC structs the bridge writes into. See
// fabric_layout_test.go for why the expected sizes are constants rather than
// C.sizeof_* reads.
//
// The SRAM status size matters twice over: the bridge writes twelve fields into
// a caller-allocated buffer, and the size is half of the version tag the caller
// must present, so drift would turn every SRAM query into a version mismatch.

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
//	EccSramErrorStatus: version(4) + 4 pad + 11 * 8 + bThresholdExceeded(4)
//	                    + 4 trailing pad = 104
//	RowRemapperHistogramValues: 5 * 4 = 20
const (
	expectedEccSramErrorStatusSize         uintptr = 104
	expectedRowRemapperHistogramValuesSize uintptr = 20
)

func TestECCStructLayouts(t *testing.T) {
	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"EccSramErrorStatus", unsafe.Sizeof(nvml.EccSramErrorStatus{}), expectedEccSramErrorStatusSize},
		{"EccSramErrorStatus_v1", unsafe.Sizeof(nvml.EccSramErrorStatus_v1{}), expectedEccSramErrorStatusSize},
		{
			"RowRemapperHistogramValues",
			unsafe.Sizeof(nvml.RowRemapperHistogramValues{}),
			expectedRowRemapperHistogramValuesSize,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.goSize,
				"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either go-nvml vendor or nvml_types.h",
				tc.name, tc.goSize, tc.expected)
		})
	}
}

// TestEccSramErrorStatusVersion_MatchesGoNvml pins the version tag the bridge
// demands against the one a caller built from go-nvml's STRUCT_VERSION helper
// produces. nvidia-smi stamps this field before calling; if the two ever
// disagree, every SRAM query answers ARGUMENT_VERSION_MISMATCH and the SRAM
// rows silently go back to reporting N/A.
//
// Calling the production function is what makes this a real pin: it reads
// sizeof(C.nvmlEccSramErrorStatus_t), so the C struct sits on one side of the
// comparison and go-nvml's on the other. Recomputing the tag from
// expectedEccSramErrorStatusSize instead would compare two Go-derived values and
// stay green through exactly the C-side drift described above. The size constant
// still earns its keep in TestECCStructLayouts, where it documents the
// hand-written layout that nvml_types.h is supposed to have.
func TestEccSramErrorStatusVersion_MatchesGoNvml(t *testing.T) {
	theirs := nvml.STRUCT_VERSION(nvml.EccSramErrorStatus_v1{}, 1)
	ours := sramEccErrorStatusVersion()
	require.Equal(t, theirs, ours,
		"bridge demands 0x%x, a go-nvml caller stamps 0x%x — every SRAM query would answer "+
			"ARGUMENT_VERSION_MISMATCH; reconcile nvml_types.h with the vendored go-nvml struct",
		ours, theirs)
}
