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

// Package main provides minimal NVML event set implementations.
// nvidia-smi calls nvmlEventSetCreate during initialization and fails
// if it returns an error. These are no-op implementations that allow
// nvidia-smi to proceed.

package main

/*
#include <stdlib.h>
#include "nvml_types.h"
*/
import "C"
import "unsafe"

// dummyEventSetBacking provides a stable address for the dummy event set handle.
// nvidia-smi just needs a non-null handle; it doesn't actually wait for events
// in the default (non-daemon) invocation mode.
var dummyEventSetBacking byte
var dummyEventSet = C.nvmlEventSet_t(unsafe.Pointer(&dummyEventSetBacking))

//export nvmlEventSetCreate
func nvmlEventSetCreate(set *C.nvmlEventSet_t) C.nvmlReturn_t {
	if set == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	*set = dummyEventSet
	return C.NVML_SUCCESS
}

//export nvmlEventSetFree
func nvmlEventSetFree(set C.nvmlEventSet_t) C.nvmlReturn_t {
	return C.NVML_SUCCESS
}

//export nvmlDeviceRegisterEvents
func nvmlDeviceRegisterEvents(device C.nvmlDevice_t, eventTypes C.ulonglong, set C.nvmlEventSet_t) C.nvmlReturn_t {
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSupportedEventTypes
func nvmlDeviceGetSupportedEventTypes(device C.nvmlDevice_t, eventTypes *C.ulonglong) C.nvmlReturn_t {
	if eventTypes == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Report no supported events
	*eventTypes = 0
	return C.NVML_SUCCESS
}
