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
#define NVML_ERROR_UNINITIALIZED        1
#define NVML_ERROR_INVALID_ARGUMENT     2
#define NVML_ERROR_NOT_SUPPORTED        3
#define NVML_ERROR_INSUFFICIENT_SIZE    7
#define NVML_ERROR_UNKNOWN              999
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func main() {
	// This is required for buildmode=c-shared
}

//export nvmlInit_v2
func nvmlInit_v2() C.nvmlReturn_t {
	ret := engine.GetEngine().Init()
	return C.nvmlReturn_t(ret)
}

//export nvmlInit
func nvmlInit() C.nvmlReturn_t {
	return nvmlInit_v2()
}

//export nvmlShutdown
func nvmlShutdown() C.nvmlReturn_t {
	ret := engine.GetEngine().Shutdown()
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2(deviceCount *C.uint) C.nvmlReturn_t {
	if deviceCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	count, ret := engine.GetEngine().DeviceGetCount()
	if ret == nvml.SUCCESS {
		*deviceCount = C.uint(count)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetCount
func nvmlDeviceGetCount(deviceCount *C.uint) C.nvmlReturn_t {
	return nvmlDeviceGetCount_v2(deviceCount)
}

//export nvmlDeviceGetHandleByIndex_v2
func nvmlDeviceGetHandleByIndex_v2(index C.uint, device *C.nvmlDevice_t) C.nvmlReturn_t {
	if device == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	handle, ret := engine.GetEngine().DeviceGetHandleByIndex(int(index))
	if ret == nvml.SUCCESS {
		*device = C.nvmlDevice_t(uintptr(handle))
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetHandleByIndex
func nvmlDeviceGetHandleByIndex(index C.uint, device *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByIndex_v2(index, device)
}

//export nvmlDeviceGetHandleByUUID
func nvmlDeviceGetHandleByUUID(uuid *C.char, device *C.nvmlDevice_t) C.nvmlReturn_t {
	if uuid == nil || device == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	uuidStr := C.GoString(uuid)
	handle, ret := engine.GetEngine().DeviceGetHandleByUUID(uuidStr)
	if ret == nvml.SUCCESS {
		*device = C.nvmlDevice_t(uintptr(handle))
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetHandleByPciBusId_v2
func nvmlDeviceGetHandleByPciBusId_v2(pciBusId *C.char, device *C.nvmlDevice_t) C.nvmlReturn_t {
	if pciBusId == nil || device == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	pciBusIdStr := C.GoString(pciBusId)
	handle, ret := engine.GetEngine().DeviceGetHandleByPciBusId(pciBusIdStr)
	if ret == nvml.SUCCESS {
		*device = C.nvmlDevice_t(uintptr(handle))
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetHandleByPciBusId
func nvmlDeviceGetHandleByPciBusId(pciBusId *C.char, device *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByPciBusId_v2(pciBusId, device)
}

//export nvmlSystemGetDriverVersion
func nvmlSystemGetDriverVersion(version *C.char, length C.uint) C.nvmlReturn_t {
	if version == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	verStr, ret := engine.GetEngine().SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	if len(verStr)+1 > int(length) {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	// Copy string to C buffer
	cStr := C.CString(verStr)
	defer C.free(unsafe.Pointer(cStr))
	C.strncpy(version, cStr, C.size_t(length))

	return C.NVML_SUCCESS
}

//export nvmlSystemGetNVMLVersion
func nvmlSystemGetNVMLVersion(version *C.char, length C.uint) C.nvmlReturn_t {
	if version == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	verStr, ret := engine.GetEngine().SystemGetNVMLVersion()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	if len(verStr)+1 > int(length) {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	// Copy string to C buffer
	cStr := C.CString(verStr)
	defer C.free(unsafe.Pointer(cStr))
	C.strncpy(version, cStr, C.size_t(length))

	return C.NVML_SUCCESS
}

//export nvmlSystemGetCudaDriverVersion
func nvmlSystemGetCudaDriverVersion(cudaDriverVersion *C.int) C.nvmlReturn_t {
	if cudaDriverVersion == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	ver, ret := engine.GetEngine().SystemGetCudaDriverVersion()
	if ret == nvml.SUCCESS {
		*cudaDriverVersion = C.int(ver)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlSystemGetCudaDriverVersion_v2
func nvmlSystemGetCudaDriverVersion_v2(cudaDriverVersion *C.int) C.nvmlReturn_t {
	return nvmlSystemGetCudaDriverVersion(cudaDriverVersion)
}

// Static buffer for error strings to avoid memory leaks
var errorStringBuf [256]C.char

//export nvmlErrorString
func nvmlErrorString(result C.nvmlReturn_t) *C.char {
	var msg string
	switch result {
	case C.NVML_SUCCESS:
		msg = "Success"
	case C.NVML_ERROR_UNINITIALIZED:
		msg = "Uninitialized"
	case C.NVML_ERROR_INVALID_ARGUMENT:
		msg = "Invalid Argument"
	case C.NVML_ERROR_NOT_SUPPORTED:
		msg = "Not Supported"
	case C.NVML_ERROR_INSUFFICIENT_SIZE:
		msg = "Insufficient Size"
	default:
		msg = "Unknown Error"
	}
	
	// Use static buffer to avoid memory leak
	cStr := C.CString(msg)
	C.strncpy(&errorStringBuf[0], cStr, 255)
	C.free(unsafe.Pointer(cStr))
	errorStringBuf[255] = 0
	
	return &errorStringBuf[0]
}

