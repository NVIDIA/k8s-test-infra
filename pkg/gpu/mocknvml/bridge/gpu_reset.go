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
	"strconv"
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
// the mock that state is exactly what `nvml-mock-ctl` injected. So the same
// mutation `nvml-mock-ctl reset --gpu <n>` performs is the faithful analogue,
// and it is the same code path: mockctl.Doc.Reset under mockctl's lock.
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
	if err := clearDeviceOverrides(engine.ConfigOverridePath(), index); err != nil {
		debugLog("[RESET] GPU %d: %v\n", index, err)
		return 0
	}
	debugLog("[RESET] GPU %d returned to its healthy baseline\n", index)
	return 1
}

// clearDeviceOverrides removes the device's override bucket, taking the same
// flock nvml-mock-ctl takes so a concurrent injection cannot interleave with the
// reset.
//
// The shared `all:` bucket is deliberately left alone, which matches
// `nvml-mock-ctl reset --gpu <n>`: the schema cannot express "drop the shared
// block for one GPU only". State injected with `--gpu all` therefore survives a
// reset of any single GPU, and survives a bare `nvidia-smi --gpu-reset` too,
// because nvidia-smi drives that as one per-device reset per GPU rather than as
// one all-GPU operation. Clearing it needs `nvml-mock-ctl reset --gpu all`.
//
// A device with nothing injected is reset without touching the file at all — no
// write and no lock. That is not just an optimisation: resetting a healthy GPU is
// the common case, and skipping the write keeps it working where the overrides
// are not writable. Only a reset that genuinely has state to clear can fail.
func clearDeviceOverrides(path string, index int) error {
	if path == "" {
		// No config, so no injected state to clear; the device is already at its
		// baseline and the reset has nothing to do.
		return nil
	}
	if has, err := hasDeviceOverrides(path, index); err != nil || !has {
		return err
	}

	unlock, err := mockctl.LockOverride(path)
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read under the lock: the peek above raced with any concurrent writer.
	doc, err := mockctl.Load(path)
	if err != nil {
		return err
	}
	doc.Reset(mockctl.Target{Index: index})
	return mockctl.WriteAtomic(path, doc)
}

// hasDeviceOverrides reports whether the document carries a bucket for the
// device. A missing or empty file is not an error: it means nothing was injected.
func hasDeviceOverrides(path string, index int) (bool, error) {
	doc, err := mockctl.Load(path)
	if err != nil {
		return false, err
	}
	_, ok := doc.Devices[strconv.Itoa(index)]
	return ok, nil
}
