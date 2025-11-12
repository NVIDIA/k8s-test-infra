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

#define NVML_SUCCESS                0
#define NVML_ERROR_INVALID_ARGUMENT 2

#define NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE 32
#define NVML_DEVICE_PCI_BUS_ID_BUFFER_V2_SIZE 16

typedef struct {
    char busIdLegacy[NVML_DEVICE_PCI_BUS_ID_BUFFER_V2_SIZE];
    unsigned int domain;
    unsigned int bus;
    unsigned int device;
    unsigned int pciDeviceId;
    unsigned int pciSubSystemId;
    char busId[NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE];
} nvmlPciInfo_t;
*/
import "C"
import (
	"bytes"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetPciInfo
func nvmlDeviceGetPciInfo(device C.nvmlDevice_t, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetPciInfo_v3(device, pci)
}

//export nvmlDeviceGetPciInfo_v2
func nvmlDeviceGetPciInfo_v2(device C.nvmlDevice_t, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetPciInfo_v3(device, pci)
}

//export nvmlDeviceGetPciInfo_v3
func nvmlDeviceGetPciInfo_v3(device C.nvmlDevice_t, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	if pci == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	pciInfo, ret := dev.GetPciInfo()
	if ret != nvml.SUCCESS {
		return C.nvmlReturn_t(ret)
	}

	// Safely convert BusId byte array to string
	// Find null terminator to avoid reading past array bounds
	busIDBytes := pciInfo.BusId[:]
	nullIdx := bytes.IndexByte(busIDBytes, 0)
	var busIDStr string
	if nullIdx >= 0 {
		busIDStr = string(busIDBytes[:nullIdx])
	} else {
		// No null terminator found, use entire array
		busIDStr = string(busIDBytes)
	}
	
	// Copy bus ID to both fields
	busIDCStr := C.CString(busIDStr)
	defer C.free(unsafe.Pointer(busIDCStr))
	
	C.strncpy(&pci.busId[0], busIDCStr, C.NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE)
	C.strncpy(&pci.busIdLegacy[0], busIDCStr, C.NVML_DEVICE_PCI_BUS_ID_BUFFER_V2_SIZE)

	// Copy numeric fields
	pci.domain = C.uint(pciInfo.Domain)
	pci.bus = C.uint(pciInfo.Bus)
	pci.device = C.uint(pciInfo.Device)
	pci.pciDeviceId = C.uint(pciInfo.PciDeviceId)
	pci.pciSubSystemId = C.uint(pciInfo.PciSubSystemId)

	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCudaComputeCapability
func nvmlDeviceGetCudaComputeCapability(device C.nvmlDevice_t, major *C.int, minor *C.int) C.nvmlReturn_t {
	if major == nil || minor == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	maj, min, ret := dev.GetCudaComputeCapability()
	if ret == nvml.SUCCESS {
		*major = C.int(maj)
		*minor = C.int(min)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetBrand
func nvmlDeviceGetBrand(device C.nvmlDevice_t, brandType *C.int) C.nvmlReturn_t {
	if brandType == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	brand, ret := dev.GetBrand()
	if ret == nvml.SUCCESS {
		*brandType = C.int(brand)
	}
	return C.nvmlReturn_t(ret)
}

//export nvmlDeviceGetArchitecture
func nvmlDeviceGetArchitecture(device C.nvmlDevice_t, arch *C.int) C.nvmlReturn_t {
	if arch == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	dev := engine.GetEngine().GetDevice(uintptr(device))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	architecture, ret := dev.GetArchitecture()
	if ret == nvml.SUCCESS {
		*arch = C.int(architecture)
	}
	return C.nvmlReturn_t(ret)
}

