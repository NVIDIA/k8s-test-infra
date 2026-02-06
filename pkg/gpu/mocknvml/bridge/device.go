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

// Package main provides NVML device handle functions.
// This file contains the hand-written implementations for:
// - nvmlDeviceGetCount, nvmlDeviceGetCount_v1, nvmlDeviceGetCount_v2
// - nvmlDeviceGetHandleByIndex, nvmlDeviceGetHandleByIndex_v1, nvmlDeviceGetHandleByIndex_v2
// - nvmlDeviceGetHandleByUUID
// - nvmlDeviceGetHandleByPciBusId, nvmlDeviceGetHandleByPciBusId_v1, nvmlDeviceGetHandleByPciBusId_v2
// - nvmlDeviceGetName
// - nvmlDeviceGetUUID
// - nvmlDeviceGetIndex
// - nvmlDeviceGetBrand
// - nvmlDeviceGetSerial

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// =============================================================================
// Device Count Functions
// =============================================================================

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2(deviceCount unsafe.Pointer) C.nvmlReturn_t {
	if deviceCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	count, ret := engine.GetEngine().DeviceGetCount()
	if ret == nvml.SUCCESS {
		*(*C.uint)(deviceCount) = C.uint(count)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetCount
func nvmlDeviceGetCount(deviceCount unsafe.Pointer) C.nvmlReturn_t {
	return nvmlDeviceGetCount_v2(deviceCount)
}

//export nvmlDeviceGetCount_v1
func nvmlDeviceGetCount_v1(deviceCount unsafe.Pointer) C.nvmlReturn_t {
	return nvmlDeviceGetCount_v2(deviceCount)
}

// =============================================================================
// Device Handle Functions
// =============================================================================

//export nvmlDeviceGetHandleByIndex_v2
func nvmlDeviceGetHandleByIndex_v2(index C.uint, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if nvmlDevice == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	debugLog("[NVML] nvmlDeviceGetHandleByIndex(%d)\n", index)
	handle, ret := engine.GetEngine().DeviceGetHandleByIndex(int(index))
	if ret == nvml.SUCCESS {
		// nvmlDevice_t is a struct with a handle field pointing to opaque nvmlDevice_st
		//nolint:govet // Converting uintptr to unsafe.Pointer is intentional - handle was allocated
		// as C memory by HandleTable.Register() and we need to pass it back to the C caller
		nvmlDevice.handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(handle))
		debugLog("[NVML]   -> handle=0x%x ret=%d\n", uintptr(handle), ret)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByIndex
func nvmlDeviceGetHandleByIndex(index C.uint, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByIndex_v2(index, nvmlDevice)
}

//export nvmlDeviceGetHandleByIndex_v1
func nvmlDeviceGetHandleByIndex_v1(index C.uint, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByIndex_v2(index, nvmlDevice)
}

//export nvmlDeviceGetHandleByUUID
func nvmlDeviceGetHandleByUUID(uuid *C.char, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if nvmlDevice == nil || uuid == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	goUUID := C.GoString(uuid)
	handle, ret := engine.GetEngine().DeviceGetHandleByUUID(goUUID)
	if ret == nvml.SUCCESS {
		//nolint:govet // Converting uintptr to unsafe.Pointer is intentional - handle was allocated
		// as C memory by HandleTable.Register() and we need to pass it back to the C caller
		nvmlDevice.handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(handle))
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByPciBusId_v2
func nvmlDeviceGetHandleByPciBusId_v2(pciBusId *C.char, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if nvmlDevice == nil || pciBusId == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	goPciBusId := C.GoString(pciBusId)
	handle, ret := engine.GetEngine().DeviceGetHandleByPciBusId(goPciBusId)
	if ret == nvml.SUCCESS {
		//nolint:govet // Converting uintptr to unsafe.Pointer is intentional - handle was allocated
		// as C memory by HandleTable.Register() and we need to pass it back to the C caller
		nvmlDevice.handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(handle))
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByPciBusId_v1
func nvmlDeviceGetHandleByPciBusId_v1(pciBusId *C.char, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByPciBusId_v2(pciBusId, nvmlDevice)
}

// =============================================================================
// Device Info Functions (Basic)
// =============================================================================

//export nvmlDeviceGetName
func nvmlDeviceGetName(nvmlDevice C.nvmlDevice_t, name *C.char, length C.uint) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	devName, ret := dev.GetName()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(devName, name, length)
}

//export nvmlDeviceGetUUID
func nvmlDeviceGetUUID(nvmlDevice C.nvmlDevice_t, uuid *C.char, length C.uint) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	devUUID, ret := dev.GetUUID()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(devUUID, uuid, length)
}

//export nvmlDeviceGetIndex
func nvmlDeviceGetIndex(nvmlDevice C.nvmlDevice_t, index *C.uint) C.nvmlReturn_t {
	if index == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	idx, ret := dev.GetIndex()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*index = C.uint(idx)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetBrand
func nvmlDeviceGetBrand(nvmlDevice C.nvmlDevice_t, _type *C.nvmlBrandType_t) C.nvmlReturn_t {
	if _type == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	brand, ret := dev.GetBrand()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*_type = C.nvmlBrandType_t(brand)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSerial
func nvmlDeviceGetSerial(nvmlDevice C.nvmlDevice_t, serial *C.char, length C.uint) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	devSerial, ret := dev.GetSerial()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(devSerial, serial, length)
}
