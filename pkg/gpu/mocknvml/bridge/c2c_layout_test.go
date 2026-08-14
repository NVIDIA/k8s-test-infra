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
// files for packages containing //export, so the expected size is a constant
// derived from the C layout in nvml_types.h.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// nvmlC2cModeInfo_v1_t is a single unsigned int — no version tag, despite the
// V suffix on nvmlDeviceGetC2cModeInfoV.
const expectedC2cModeInfoV1Size uintptr = 4

func TestC2cModeInfoStructLayout(t *testing.T) {
	require.Equal(t, expectedC2cModeInfoV1Size, unsafe.Sizeof(nvml.C2cModeInfo_v1{}),
		"C2cModeInfo_v1: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either go-nvml vendor or nvml_types.h",
		unsafe.Sizeof(nvml.C2cModeInfo_v1{}), expectedC2cModeInfoV1Size)
}

// TestC2cModeInfoVersionTagIsPinned pins NVML's version macro for this struct
// even though the struct has nowhere to carry it, which is why the bridge does
// no version dispatch. STRUCT_VERSION encodes sizeof in the low 24 bits, so
// this value moves the moment the struct grows — exactly what would happen if
// a future go-nvml added the version field the function name implies. That is
// the signal to revisit the bridge implementation.
func TestC2cModeInfoVersionTagIsPinned(t *testing.T) {
	const wantTag = uint32(expectedC2cModeInfoV1Size) | (1 << 24)
	require.Equal(t, wantTag, nvml.STRUCT_VERSION(nvml.C2cModeInfo_v1{}, 1),
		"C2cModeInfo_v1 version tag changed — the struct layout moved; re-check whether nvmlDeviceGetC2cModeInfoV now needs version dispatch")
}
