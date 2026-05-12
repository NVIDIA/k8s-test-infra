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

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// TestClassifyFabricVersion locks in the strict dispatch contract used
// by nvmlDeviceGetGpuFabricInfoV: only the two known version tags
// (v2 and v3) select a layout, everything else — including zero — is
// invalid and must result in NVML_ERROR_ARGUMENT_VERSION_MISMATCH on
// the caller's side.
func TestClassifyFabricVersion(t *testing.T) {
	v2Tag := FabricStructVersion(unsafe.Sizeof(nvml.GpuFabricInfo_v2{}), 2)
	v3Tag := FabricStructVersion(unsafe.Sizeof(nvml.GpuFabricInfo_v3{}), 3)
	cases := []struct {
		name      string
		requested uint32
		want      FabricVersionKind
	}{
		{"v2 tag", v2Tag, FabricVersionV2},
		{"v3 tag", v3Tag, FabricVersionV3},
		// Zero is the specific bug we are guarding against: a caller
		// that forgot to set Version must NOT be silently treated as
		// v3 — that path could overrun a v2-sized buffer cast to
		// *nvmlGpuFabricInfoV_t.
		{"zero (unset version field)", 0, FabricVersionInvalid},
		// A v1-sized tag with version=1 has never been valid for the
		// V entry point; only v2/v3 are.
		{"v1-sized tag", FabricStructVersion(unsafe.Sizeof(nvml.GpuFabricInfo{}), 1), FabricVersionInvalid},
		// Garbage.
		{"garbage 0xdeadbeef", 0xdeadbeef, FabricVersionInvalid},
		// A v2 size encoded with version=3 is not a valid tag either:
		// real NVML's STRUCT_VERSION encodes size AND version, so a
		// size/version mismatch means the caller is confused.
		{"v2 size with v3 version", FabricStructVersion(unsafe.Sizeof(nvml.GpuFabricInfo_v2{}), 3), FabricVersionInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFabricVersion(tc.requested, v2Tag, v3Tag)
			if got != tc.want {
				t.Errorf("ClassifyFabricVersion(0x%x, v2=0x%x, v3=0x%x) = %d, want %d",
					tc.requested, v2Tag, v3Tag, got, tc.want)
			}
		})
	}
}
