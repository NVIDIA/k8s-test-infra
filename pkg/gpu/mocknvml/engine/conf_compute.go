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

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// Confidential Compute memory reporting.
//
// Both getters answer zero on every device, unconditionally, and neither reads
// config. That is what real hardware does: every nvidia-smi capture in
// tests/e2e/go/assertions/nvidiasmi/testdata/hardware reports 0 MiB across the
// whole block on drivers 570 and later, including the A100, L40S and T4 boards
// that cannot do Confidential Compute at all. Nothing partitions protected
// memory until CC mode is switched on, and NVML answers "none" rather than
// "unsupported" whether or not the part could.
//
// So there is deliberately no capability gate here. Gating on
// features.confidential_compute would have made a T4 report N/A where a real one
// reports 0 MiB, and CC mode itself is unmodelled until issue #377. What does
// gate the surface is the driver version: the CC APIs arrived in 525, and
// version.go answers FUNCTION_NOT_FOUND below that, as a real older driver does.
//
// Neither getter advances the failure-injection counter. nvidia-smi calls both
// once per GPU on every -q run, so ticking here would make an "after_calls"
// scenario trip on a schedule that depends on how much of nvidia-smi's output
// the caller asked for. They still surface a lost GPU, matching the other
// per-query reads that answer from static state (platform info, C2C mode).

// GetConfComputeMemSizeInfo reports how device memory is split between the
// protected and unprotected halves of a Confidential Compute partition,
// backing nvmlDeviceGetConfComputeMemSizeInfo. Both halves are zero while CC
// mode is off, which is every profile.
func (d *ConfigurableDevice) GetConfComputeMemSizeInfo() (nvml.ConfComputeMemSizeInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.ConfComputeMemSizeInfo{}, ret
	}
	debugLog("[NVML] nvmlDeviceGetConfComputeMemSizeInfo -> protected=0 KiB unprotected=0 KiB\n")
	return nvml.ConfComputeMemSizeInfo{}, nvml.SUCCESS
}

// GetConfComputeProtectedMemoryUsage reports occupancy of the protected memory
// region, backing nvmlDeviceGetConfComputeProtectedMemoryUsage and the "Conf
// Compute Protected Memory Usage" block of `nvidia-smi -q`. With CC mode off
// there is no protected region, so total, used and free are all zero.
func (d *ConfigurableDevice) GetConfComputeProtectedMemoryUsage() (nvml.Memory, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.Memory{}, ret
	}
	debugLog("[NVML] nvmlDeviceGetConfComputeProtectedMemoryUsage -> total=0 used=0 free=0\n")
	return nvml.Memory{}, nvml.SUCCESS
}
