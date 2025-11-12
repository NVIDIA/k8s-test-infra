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

typedef struct {
    unsigned long long total;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_t;

typedef struct {
    unsigned long long total;
    unsigned long long reserved;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_v2_t;

typedef struct {
    unsigned long long bar1Total;
    unsigned long long bar1Free;
    unsigned long long bar1Used;
} nvmlBAR1Memory_t;
*/
import "C"
import (
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetMemoryInfo
func nvmlDeviceGetMemoryInfo(device C.nvmlDevice_t, memory *C.nvmlMemory_t) C.nvmlReturn_t {
	if memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	memInfo, ret := dev.GetMemoryInfo()
	if ret == nvml.SUCCESS {
		memory.total = C.ulonglong(memInfo.Total)
		memory.free = C.ulonglong(memInfo.Free)
		memory.used = C.ulonglong(memInfo.Used)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetMemoryInfo_v2
func nvmlDeviceGetMemoryInfo_v2(device C.nvmlDevice_t, memory *C.nvmlMemory_v2_t) C.nvmlReturn_t {
	if memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	memInfo, ret := dev.GetMemoryInfo()
	if ret == nvml.SUCCESS {
		memory.total = C.ulonglong(memInfo.Total)
		memory.reserved = C.ulonglong(0) // Mock doesn't track reserved
		memory.free = C.ulonglong(memInfo.Free)
		memory.used = C.ulonglong(memInfo.Used)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetBAR1MemoryInfo
func nvmlDeviceGetBAR1MemoryInfo(device C.nvmlDevice_t, bar1Memory *C.nvmlBAR1Memory_t) C.nvmlReturn_t {
	if bar1Memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	bar1Info, ret := dev.GetBAR1MemoryInfo()
	if ret == nvml.SUCCESS {
		bar1Memory.bar1Total = C.ulonglong(bar1Info.Bar1Total)
		bar1Memory.bar1Free = C.ulonglong(bar1Info.Bar1Free)
		bar1Memory.bar1Used = C.ulonglong(bar1Info.Bar1Used)
	}
	return C.nvmlReturn_t(ret)
}

