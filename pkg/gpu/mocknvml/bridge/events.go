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
typedef void* nvmlEventSet_t;
typedef unsigned long long ulonglong;

#define NVML_SUCCESS                0
#define NVML_ERROR_NOT_SUPPORTED    3
*/
import "C"
import "unsafe"

// Event-related functions (stubs - not supported in mock)

//export nvmlEventSetCreate
func nvmlEventSetCreate(set *C.nvmlEventSet_t) C.nvmlReturn_t {
	// Return not supported - mock library doesn't support event monitoring
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlDeviceRegisterEvents
func nvmlDeviceRegisterEvents(device C.nvmlDevice_t, eventTypes C.ulonglong, set C.nvmlEventSet_t) C.nvmlReturn_t {
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlEventSetWait
func nvmlEventSetWait(set C.nvmlEventSet_t, data unsafe.Pointer, timeoutms C.uint) C.nvmlReturn_t {
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlEventSetWait_v2
func nvmlEventSetWait_v2(set C.nvmlEventSet_t, data unsafe.Pointer, timeoutms C.uint) C.nvmlReturn_t {
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlEventSetFree
func nvmlEventSetFree(set C.nvmlEventSet_t) C.nvmlReturn_t {
	return C.NVML_ERROR_NOT_SUPPORTED
}

