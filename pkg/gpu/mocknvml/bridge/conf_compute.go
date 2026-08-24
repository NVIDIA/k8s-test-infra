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

// Package main provides the NVML Confidential Compute memory functions.
// nvidia-smi calls both of these once per GPU on every -q run: the protected
// memory usage backs the "Conf Compute Protected Memory Usage" block, and the
// memory size info backs the protected/unprotected split behind it.
//
// The engine answers zero on every device rather than N/A, which is what real
// NVML reports while CC mode is off. See issue NVIDIA/k8s-test-infra#711.
package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetConfComputeMemSizeInfo
func nvmlDeviceGetConfComputeMemSizeInfo(device C.nvmlDevice_t, memInfo *C.nvmlConfComputeMemSizeInfo_t) C.nvmlReturn_t {
	if memInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetConfComputeMemSizeInfo"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetConfComputeMemSizeInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	memInfo.protectedMemSizeKib = C.ulonglong(info.ProtectedMemSizeKib)
	memInfo.unprotectedMemSizeKib = C.ulonglong(info.UnprotectedMemSizeKib)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetConfComputeProtectedMemoryUsage
func nvmlDeviceGetConfComputeProtectedMemoryUsage(device C.nvmlDevice_t, memory *C.nvmlMemory_t) C.nvmlReturn_t {
	if memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetConfComputeProtectedMemoryUsage"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mem, ret := dev.GetConfComputeProtectedMemoryUsage()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	memory.total = C.ulonglong(mem.Total)
	memory.free = C.ulonglong(mem.Free)
	memory.used = C.ulonglong(mem.Used)
	return C.NVML_SUCCESS
}

func lookupConfComputeDevice(device C.nvmlDevice_t) (*engine.ConfigurableDevice, bool) {
	handle := unsafe.Pointer(device.handle)
	dev, ok := engine.GetEngine().LookupDevice(handle).(*engine.ConfigurableDevice)
	return dev, ok
}
