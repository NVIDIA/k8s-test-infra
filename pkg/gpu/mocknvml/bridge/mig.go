// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

/*
#include <stdlib.h>

typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS                0
#define NVML_ERROR_NOT_SUPPORTED 3
#define NVML_ERROR_INVALID_ARGUMENT 2
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetMigMode
func nvmlDeviceGetMigMode(device C.nvmlDevice_t, currentMode *C.uint, pendingMode *C.uint) C.nvmlReturn_t {
	if currentMode == nil || pendingMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// MIG not supported in mock
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlDeviceGetMaxMigDeviceCount
func nvmlDeviceGetMaxMigDeviceCount(device C.nvmlDevice_t, count *C.uint) C.nvmlReturn_t {
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return 0 to indicate no MIG support
	*count = 0
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetMigMode
func nvmlDeviceSetMigMode(device C.nvmlDevice_t, mode C.uint, activationStatus *C.uint) C.nvmlReturn_t {
	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// MIG configuration not supported in mock
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlDeviceGetGpuInstanceProfileInfo
func nvmlDeviceGetGpuInstanceProfileInfo(device C.nvmlDevice_t, profile C.uint, info unsafe.Pointer) C.nvmlReturn_t {
	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// MIG not supported in mock
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlDeviceGetGpuInstances
func nvmlDeviceGetGpuInstances(device C.nvmlDevice_t, profileId C.uint, instances unsafe.Pointer, count *C.uint) C.nvmlReturn_t {
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	// Return 0 instances
	*count = 0
	return C.NVML_SUCCESS
}

