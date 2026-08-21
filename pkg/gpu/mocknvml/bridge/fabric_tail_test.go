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

// Struct-tail boundary tests for nvmlDeviceGetGpuFabricInfoV.
//
// A v2 caller reaches the bridge through a buffer that is only as big as the
// v2 struct: go-nvml's GpuFabricInfoHandler.V2() declares a GpuFabricInfo_v2
// and casts it to *GpuFabricInfoV (the v3 alias) before calling. The bridge's
// v2 branch therefore may only write the fields the two layouts share — and
// that is only safe while healthSummary lives strictly past the end of a v2
// struct, and while every shared field sits at the same offset in both.
//
// These invariants were free while the summary was always zero: writing a zero
// byte past a caller's buffer is still a corruption, but a benign-looking one.
// Now that a healthy fabric reports a non-zero summary (#677), a dispatch or
// layout regression writes a visibly wrong byte into a caller's neighbouring
// memory, so the boundary is pinned here.
//
// Like the sibling layout tests, these are pure Go: the package carries
// //export directives, so cgo is unavailable in its test files. They pin the
// go-nvml structs, which TestFabricStructLayouts pins byte-for-byte against
// the hand-written C layouts in nvml_types.h.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// TestFabricHealthSummaryLiesOutsideV2 pins the tail boundary: the v3-only
// healthSummary byte must begin at or after the end of a v2 struct, so no
// write the bridge's v2 branch is allowed to make can reach it, and so the
// v3 branch running for a v2 caller would demonstrably overflow.
func TestFabricHealthSummaryLiesOutsideV2(t *testing.T) {
	v2Size := unsafe.Sizeof(nvml.GpuFabricInfo_v2{})
	summaryOffset := unsafe.Offsetof(nvml.GpuFabricInfo_v3{}.HealthSummary)

	require.Equal(t, v2Size, summaryOffset,
		"healthSummary sits at offset %d but a v2 caller's buffer ends at %d: the v2 branch's "+
			"shared-prefix assumption no longer holds and it must zero or skip the tail explicitly",
		summaryOffset, v2Size)
	require.Greater(t, unsafe.Sizeof(nvml.GpuFabricInfo_v3{}), v2Size,
		"v3 must stay strictly larger than v2, or the version dispatch has nothing to protect")
}

// TestFabricSharedFieldOffsetsMatch pins the precondition of the bridge's
// reinterpret cast in the v2 branch: every field the two versions share must
// live at the same offset. If go-nvml or nvml_types.h ever inserted a field
// into v3's prefix, the v2 branch would keep compiling and start writing each
// value into the wrong slot of the caller's struct.
func TestFabricSharedFieldOffsetsMatch(t *testing.T) {
	var v2 nvml.GpuFabricInfo_v2
	var v3 nvml.GpuFabricInfo_v3
	cases := map[string]struct{ inV2, inV3 uintptr }{
		"version":     {unsafe.Offsetof(v2.Version), unsafe.Offsetof(v3.Version)},
		"clusterUuid": {unsafe.Offsetof(v2.ClusterUuid), unsafe.Offsetof(v3.ClusterUuid)},
		"status":      {unsafe.Offsetof(v2.Status), unsafe.Offsetof(v3.Status)},
		"cliqueId":    {unsafe.Offsetof(v2.CliqueId), unsafe.Offsetof(v3.CliqueId)},
		"state":       {unsafe.Offsetof(v2.State), unsafe.Offsetof(v3.State)},
		"healthMask":  {unsafe.Offsetof(v2.HealthMask), unsafe.Offsetof(v3.HealthMask)},
	}
	for name, tc := range cases {
		require.Equal(t, tc.inV2, tc.inV3,
			"%s is at offset %d in v2 but %d in v3 — the v2 branch's reinterpret cast writes the wrong bytes",
			name, tc.inV2, tc.inV3)
	}
}

// TestClassifyFabricVersion_V2TagNeverTakesV3Path is the behavioural half of
// the boundary. The layout guarantees above only matter because the v2 tag
// must classify as v2: a v2 tag misclassified as v3 makes the bridge write
// healthSummary four bytes past a 36-byte caller buffer.
func TestClassifyFabricVersion_V2TagNeverTakesV3Path(t *testing.T) {
	v2Tag := FabricStructVersion(unsafe.Sizeof(nvml.GpuFabricInfo_v2{}), 2)
	v3Tag := FabricStructVersion(unsafe.Sizeof(nvml.GpuFabricInfo_v3{}), 3)

	require.Equal(t, FabricVersionV2, ClassifyFabricVersion(v2Tag, v2Tag, v3Tag),
		"a go-nvml V2() caller must take the v2 branch")
	require.NotEqual(t, v2Tag, v3Tag, "the two tags must be distinguishable")
}
