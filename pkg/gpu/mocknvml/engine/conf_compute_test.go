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

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// TestGetConfComputeMemSizeInfo_ZeroOnEveryDevice pins the reading real
// hardware gives on every board, CC-capable or not: an answer of zero rather
// than the N/A a generated stub produced.
func TestGetConfComputeMemSizeInfo_ZeroOnEveryDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	info, ret := dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.SUCCESS, ret,
		"the getter must answer so nvidia-smi renders 0 rather than N/A")
	require.Equal(t, nvml.ConfComputeMemSizeInfo{}, info,
		"CC mode is off, so no memory is partitioned into protected and unprotected halves")
}

// TestGetConfComputeProtectedMemoryUsage_ZeroOnEveryDevice pins the three values
// nvidia-smi renders as its "Conf Compute Protected Memory Usage" block: 0 MiB
// each, as every hardware capture in the repo reports.
func TestGetConfComputeProtectedMemoryUsage_ZeroOnEveryDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	mem, ret := dev.GetConfComputeProtectedMemoryUsage()
	require.Equal(t, nvml.SUCCESS, ret,
		"the getter must answer so nvidia-smi renders 0 MiB rather than N/A")
	require.Equal(t, nvml.Memory{}, mem,
		"with CC mode off there is no protected region, so total, used and free are all zero")
}

// TestConfComputeMemory_LostDeviceReportsGpuIsLost keeps both getters on the
// same failure-injection path as every other device export: once the driver has
// marked the GPU gone they must report the loss, not a plausible zero.
//
// Neither getter advances the failure counter, so the device is tripped through
// a guarded call first.
func TestConfComputeMemory_LostDeviceReportsGpuIsLost(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})

	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "expected the guarded call to trip the device")

	_, ret = dev.GetConfComputeMemSizeInfo()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetConfComputeMemSizeInfo")
	_, ret = dev.GetConfComputeProtectedMemoryUsage()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetConfComputeProtectedMemoryUsage")
}
