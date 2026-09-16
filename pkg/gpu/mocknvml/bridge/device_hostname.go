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

// Package main provides the per-GPU hostname pair NVML added for Blackwell:
// nvmlDeviceGetHostname_v1 / nvmlDeviceSetHostname_v1. The value round-trips
// through the device, seeded by the profile's `hostname` key, and pre-Blackwell
// profiles answer NOT_SUPPORTED the way real hardware does.
package main

/*
#include <stdlib.h>
#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetHostname_v1
func nvmlDeviceGetHostname_v1(device C.nvmlDevice_t, hostname *C.nvmlHostname_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetHostname_v1"); !ok {
		return ret
	}
	if hostname == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	name, ret := dev.GetHostname()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(name, &hostname.value[0], C.uint(len(hostname.value)))
}

//export nvmlDeviceSetHostname_v1
func nvmlDeviceSetHostname_v1(device C.nvmlDevice_t, hostname *C.nvmlHostname_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetHostname_v1"); !ok {
		return ret
	}
	if hostname == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetHostname(
		goStringFromC(&hostname.value[0], C.NVML_DEVICE_HOSTNAME_BUFFER_SIZE),
	))
}

// Test hooks: a _test.go file cannot name nvmlDevice_t or nvmlHostname_v1_t,
// so these wrap the raw engine handle and the C struct the exports expect.

func hostnameGetForTest(handle unsafe.Pointer) (string, uint32) {
	var device C.nvmlDevice_t
	device.handle = (*C.struct_nvmlDevice_st)(handle)
	var hostname C.nvmlHostname_v1_t
	status := uint32(nvmlDeviceGetHostname_v1(device, &hostname))
	return goStringFromC(&hostname.value[0], len(hostname.value)), status
}

func hostnameSetForTest(handle unsafe.Pointer, name string) uint32 {
	var device C.nvmlDevice_t
	device.handle = (*C.struct_nvmlDevice_st)(handle)
	var hostname C.nvmlHostname_v1_t
	// A name that does not fit the buffer is the caller's problem in real NVML;
	// truncating here keeps the hook honest about what reaches the export.
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&hostname.value[0])), len(hostname.value)-1), name)
	return uint32(nvmlDeviceSetHostname_v1(device, &hostname))
}
