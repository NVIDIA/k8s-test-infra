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
#include <string.h>

typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS                    0
#define NVML_ERROR_INVALID_ARGUMENT     2
#define NVML_ERROR_INSUFFICIENT_SIZE    7
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetName
func nvmlDeviceGetName(device C.nvmlDevice_t, name *C.char, length C.uint) C.nvmlReturn_t {
	if name == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	nameStr, ret := dev.GetName()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	if len(nameStr)+1 > int(length) {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	cStr := C.CString(nameStr)
	defer C.free(unsafe.Pointer(cStr))
	C.strncpy(name, cStr, C.size_t(length))

	return C.NVML_SUCCESS
}

//export nvmlDeviceGetUUID
func nvmlDeviceGetUUID(device C.nvmlDevice_t, uuid *C.char, length C.uint) C.nvmlReturn_t {
	if uuid == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	uuidStr, ret := dev.GetUUID()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	if len(uuidStr)+1 > int(length) {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	cStr := C.CString(uuidStr)
	defer C.free(unsafe.Pointer(cStr))
	C.strncpy(uuid, cStr, C.size_t(length))

	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMinorNumber
func nvmlDeviceGetMinorNumber(device C.nvmlDevice_t, minorNumber *C.uint) C.nvmlReturn_t {
	if minorNumber == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	minor, ret := dev.GetMinorNumber()
	if ret == nvml.SUCCESS {
		*minorNumber = C.uint(minor)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetIndex
func nvmlDeviceGetIndex(device C.nvmlDevice_t, index *C.uint) C.nvmlReturn_t {
	if index == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	idx, ret := dev.GetIndex()
	if ret == nvml.SUCCESS {
		*index = C.uint(idx)
	}
	return C.nvmlReturn_t(ret)
}

