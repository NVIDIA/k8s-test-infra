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
#define NVML_ERROR_INVALID_ARGUMENT 2
#define NVML_ERROR_INSUFFICIENT_SIZE 7

typedef struct {
    unsigned int pid;
    unsigned long long usedGpuMemory;
    unsigned int gpuInstanceId;
    unsigned int computeInstanceId;
} nvmlProcessInfo_t;
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetComputeRunningProcesses
func nvmlDeviceGetComputeRunningProcesses(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetComputeRunningProcesses_v3(device, infoCount, infos)
}

//export nvmlDeviceGetComputeRunningProcesses_v2
func nvmlDeviceGetComputeRunningProcesses_v2(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetComputeRunningProcesses_v3(device, infoCount, infos)
}

//export nvmlDeviceGetComputeRunningProcesses_v3
func nvmlDeviceGetComputeRunningProcesses_v3(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	if infoCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	processes, ret := dev.GetComputeRunningProcesses()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	// If infos is nil, just return the count
	if infos == nil {
		*infoCount = C.uint(len(processes))
		return C.NVML_SUCCESS
	}

	// Check if buffer is large enough
	if int(*infoCount) < len(processes) {
		*infoCount = C.uint(len(processes))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	// Copy process info to C array
	for i, proc := range processes {
		if i >= int(*infoCount) {
			break
		}
		// Access array element using pointer arithmetic
		pInfo := (*C.nvmlProcessInfo_t)(unsafe.Pointer(uintptr(unsafe.Pointer(infos)) + uintptr(i)*unsafe.Sizeof(*infos)))
		pInfo.pid = C.uint(proc.Pid)
		pInfo.usedGpuMemory = C.ulonglong(proc.UsedGpuMemory)
		pInfo.gpuInstanceId = C.uint(proc.GpuInstanceId)
		pInfo.computeInstanceId = C.uint(proc.ComputeInstanceId)
	}

	*infoCount = C.uint(len(processes))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGraphicsRunningProcesses
func nvmlDeviceGetGraphicsRunningProcesses(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetGraphicsRunningProcesses_v3(device, infoCount, infos)
}

//export nvmlDeviceGetGraphicsRunningProcesses_v2
func nvmlDeviceGetGraphicsRunningProcesses_v2(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetGraphicsRunningProcesses_v3(device, infoCount, infos)
}

//export nvmlDeviceGetGraphicsRunningProcesses_v3
func nvmlDeviceGetGraphicsRunningProcesses_v3(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	if infoCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	processes, ret := dev.GetGraphicsRunningProcesses()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	// If infos is nil, just return the count
	if infos == nil {
		*infoCount = C.uint(len(processes))
		return C.NVML_SUCCESS
	}

	// Check if buffer is large enough
	if int(*infoCount) < len(processes) {
		*infoCount = C.uint(len(processes))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	// Copy process info to C array
	for i, proc := range processes {
		if i >= int(*infoCount) {
			break
		}
		pInfo := (*C.nvmlProcessInfo_t)(unsafe.Pointer(uintptr(unsafe.Pointer(infos)) + uintptr(i)*unsafe.Sizeof(*infos)))
		pInfo.pid = C.uint(proc.Pid)
		pInfo.usedGpuMemory = C.ulonglong(proc.UsedGpuMemory)
		pInfo.gpuInstanceId = C.uint(proc.GpuInstanceId)
		pInfo.computeInstanceId = C.uint(proc.ComputeInstanceId)
	}

	*infoCount = C.uint(len(processes))
	return C.NVML_SUCCESS
}

