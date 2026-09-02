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

// The GPU reset behind `nvidia-smi --gpu-reset` / `-r`. nvidia-smi drives the
// reset through the internal export table (see MOCK_SLOT_GPU_RESET in
// internal.go), not through any public NVML entry point, so this is reachable
// only from that dispatcher.
package main

/*
#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// mockInternalResetGPU returns the device nvidia-smi is resetting to its healthy
// baseline by dropping the overrides injected onto it, and reports 1 when the
// device is now clean.
//
// Clearing the overrides is the reset a mock can honour. A real reset clears the
// GPU's transient hardware and software error state — the double-bit ECC error
// or hung channel that made an operator reach for it in the first place — and on
// the mock that state is exactly what `nvml-mock-ctl` injected. So this performs
// the same mutation `nvml-mock-ctl reset --gpu <n>` performs, through the same
// function, rather than a second implementation of it.
//
// Scope follows nvidia-smi rather than being decided here. It calls this once per
// GPU being reset, so `-i 1` clears one device and a bare `--gpu-reset` walks
// every device in turn, which is what nvidia-smi documents for a reset with no
// -i switch.
//
//export mockInternalResetGPU
func mockInternalResetGPU(handle unsafe.Pointer) C.int {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		debugLog("[RESET] unknown device handle %p\n", handle)
		return 0
	}
	index, ret := dev.GetIndex()
	if ret != nvml.SUCCESS {
		debugLog("[RESET] device %p reports no index (ret=%d)\n", handle, ret)
		return 0
	}
	// engine.ConfigOverridePath resolves the same file the engine reads, so the
	// reader and this writer can never disagree on which one is authoritative.
	if err := mockctl.ResetDevice(engine.ConfigOverridePath(), index); err != nil {
		debugLog("[RESET] GPU %d: %v\n", index, err)
		return 0
	}
	debugLog("[RESET] GPU %d returned to its healthy baseline\n", index)
	return 1
}
