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

// Layout test for the C2C mode struct the bridge writes into. Same rationale
// and same cgo-free approach as fabric_layout_test.go: Go forbids cgo in test
// files for packages containing //export, so the expected size is a hard-coded
// constant rather than a C.sizeof_* read.
//
// This test therefore only guards the vendored go-nvml side. The C side is
// guarded independently by a _Static_assert next to nvmlC2cModeInfo_v1_t in
// nvml_types.h; the two must agree on the same number, or the bridge writes a
// different number of bytes than the caller allocated.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// nvmlC2cModeInfo_v1_t is a single unsigned int — no version tag, despite the
// V suffix on nvmlDeviceGetC2cModeInfoV. If a future go-nvml bump grows the
// struct to carry the version field that suffix implies, this size moves and
// the bridge implementation needs revisiting: it deliberately does no version
// dispatch today because there is nowhere for a caller to pass a version.
const expectedC2cModeInfoV1Size uintptr = 4

func TestC2cModeInfoStructLayout(t *testing.T) {
	require.Equal(t, expectedC2cModeInfoV1Size, unsafe.Sizeof(nvml.C2cModeInfo_v1{}),
		"C2cModeInfo_v1: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either go-nvml vendor or nvml_types.h",
		unsafe.Sizeof(nvml.C2cModeInfo_v1{}), expectedC2cModeInfoV1Size)
}
