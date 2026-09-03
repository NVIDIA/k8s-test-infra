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
	"github.com/stretchr/testify/require"
)

// platformStructSize is the size nvml_types.h asserts for both
// nvmlPlatformInfo_v1_t and nvmlPlatformInfo_v2_t. The _Static_assert there
// guards the C side; this constant carries the same number into the Go tests,
// which cannot use cgo in this package.
const platformStructSize = 44

// The version tags the bridge accepts are computed from the struct size, so a
// go-nvml bump that grew or shrank the struct would silently move every tag
// and make nvidia-smi's request classify as Invalid — the whole Platform Info
// block would go back to N/A with nothing failing. Pin the size against the
// go-nvml structs the callers actually allocate.
func TestPlatformInfoStructSizeIsPinned(t *testing.T) {
	require.Equal(t, uintptr(platformStructSize), unsafe.Sizeof(nvml.PlatformInfo_v1{}),
		"nvml.PlatformInfo_v1 size; update nvml_types.h and this constant together")
	require.Equal(t, uintptr(platformStructSize), unsafe.Sizeof(nvml.PlatformInfo_v2{}),
		"nvml.PlatformInfo_v2 size; update nvml_types.h and this constant together")
	require.Equal(t, unsafe.Sizeof(nvml.PlatformInfo_v2{}), unsafe.Sizeof(nvml.PlatformInfo{}),
		"nvml.PlatformInfo must alias the v2 layout")
}

// TestClassifyPlatformVersion locks in the strict dispatch contract used by
// nvmlDeviceGetPlatformInfo: only the two known tags select a layout, and
// everything else — including a caller that never set Version — must result in
// NVML_ERROR_ARGUMENT_VERSION_MISMATCH.
func TestClassifyPlatformVersion(t *testing.T) {
	v1Tag := FabricStructVersion(platformStructSize, 1)
	v2Tag := FabricStructVersion(platformStructSize, 2)
	require.NotEqual(t, v1Tag, v2Tag, "equal-sized structs must still get distinct tags")

	for name, tc := range map[string]struct {
		requested uint32
		want      PlatformVersionKind
	}{
		"v1 tag":                     {v1Tag, PlatformVersionV1},
		"v2 tag":                     {v2Tag, PlatformVersionV2},
		"zero (unset version field)": {0, PlatformVersionInvalid},
		"garbage 0xdeadbeef":         {0xdeadbeef, PlatformVersionInvalid},
		// STRUCT_VERSION encodes size and version together, so a right
		// version byte over a wrong size means the caller is compiled
		// against a struct the bridge cannot safely fill.
		"v2 version with wrong size": {FabricStructVersion(platformStructSize+8, 2), PlatformVersionInvalid},
		"unsupported v3 tag":         {FabricStructVersion(platformStructSize, 3), PlatformVersionInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			got := ClassifyPlatformVersion(tc.requested, v1Tag, v2Tag)
			require.Equal(t, tc.want, got,
				"ClassifyPlatformVersion(0x%x, v1=0x%x, v2=0x%x)", tc.requested, v1Tag, v2Tag)
		})
	}
}
