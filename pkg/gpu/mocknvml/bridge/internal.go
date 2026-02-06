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

// Package main provides internal NVML functions for nvidia-smi compatibility.
// This file contains the hand-written implementation for:
// - nvmlInternalGetExportTable (internal API used by nvidia-smi)

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"

// Forward declaration of our device handle getter (defined in device.go)
extern nvmlReturn_t nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t* device);

// Debug mode - check MOCK_NVML_DEBUG env var once at startup
static int debugChecked = 0;
static int debugEnabled = 0;

static int isDebugEnabled() {
    if (!debugChecked) {
        debugChecked = 1;
        debugEnabled = (getenv("MOCK_NVML_DEBUG") != NULL);
    }
    return debugEnabled;
}

// C stub function for internal export table
// This gets called by nvidia-smi via the export table function pointers
// Pattern observed: arg0=device_index (0-7), arg1=output_ptr, arg2=0x22 (flags), arg3=our_stub_ptr
static nvmlReturn_t internalStubFunction(void* arg0, void* arg1, void* arg2, void* arg3) {
    // Interpret as device index lookup
    uintptr_t rawArg0 = (uintptr_t)arg0;
    uintptr_t rawArg2 = (uintptr_t)arg2;

    // Device index lookup: small index, valid stack pointer, flags 0x22
    if (rawArg0 < 32 && rawArg2 == 0x22) {
        nvmlDevice_t* outputPtr = (nvmlDevice_t*)arg1;
        if (outputPtr != NULL) {
            nvmlReturn_t ret = nvmlDeviceGetHandleByIndex_v2((unsigned int)rawArg0, outputPtr);
            if (isDebugEnabled()) {
                fprintf(stderr, "[C-STUB] DeviceGetHandle(%u) -> ret=%d, handle=%p\n",
                        (unsigned int)rawArg0, ret, (void*)outputPtr->handle);
            }
            return ret;
        }
    }

    // Non-device call - return SUCCESS to acknowledge
    return NVML_SUCCESS;
}

// Get address of stub function
static void* getInternalStubAddress() {
    return (void*)internalStubFunction;
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// Internal export table for nvidia-smi compatibility
// Based on reverse engineering: table[0] = size (must be > 648), table[648/8] = function pointer
// Table needs to be large enough for all offsets nvidia-smi might access
var internalExportTable [256]uintptr
var exportTableInitialized = false

func initExportTable() {
	if exportTableInitialized {
		return
	}
	// Set up the export table
	// Offset 0: size field (must be > 648 = 0x288)
	internalExportTable[0] = 0x1000

	// Get function pointer for our C stub
	stubPtr := uintptr(C.getInternalStubAddress())
	debugLog("[MOCK-NVML] C stub function address: %p\n", C.getInternalStubAddress())

	// Fill all potential function pointer slots with the stub
	for i := 1; i < 256; i++ {
		internalExportTable[i] = stubPtr
	}

	exportTableInitialized = true
	debugLog("[MOCK-NVML] Export table initialized with stub at 0x%x\n", uintptr(stubPtr))
}

// Internal function used by nvidia-smi for version verification
// This is a proprietary NVIDIA internal API that nvidia-smi calls to verify
// compatibility between nvidia-smi and the NVML library.
//
//export nvmlInternalGetExportTable
func nvmlInternalGetExportTable(ppExportTable unsafe.Pointer, guid unsafe.Pointer) C.nvmlReturn_t {
	// Initialize export table on first call
	initExportTable()

	// Debug: print the GUID being requested
	var guidStr string
	if guid != nil {
		guidBytes := (*[16]byte)(guid)
		guidStr = fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
			guidBytes[3], guidBytes[2], guidBytes[1], guidBytes[0],
			guidBytes[5], guidBytes[4],
			guidBytes[7], guidBytes[6],
			guidBytes[8], guidBytes[9],
			guidBytes[10], guidBytes[11], guidBytes[12], guidBytes[13], guidBytes[14], guidBytes[15])
		debugLog("[MOCK-NVML] nvmlInternalGetExportTable called with GUID: %s\n", guidStr)
	}

	// Only handle the known GUID for device enumeration
	knownGUID := "6c3efec4-8fc9-4e6c-a327-ee696e12f7c4"
	if guidStr != knownGUID {
		debugLog("[MOCK-NVML] Unknown GUID %s - returning NOT_SUPPORTED\n", guidStr)
		return C.NVML_ERROR_NOT_SUPPORTED
	}

	// Return the internal export table
	if ppExportTable != nil {
		*(*unsafe.Pointer)(ppExportTable) = unsafe.Pointer(&internalExportTable[0])
	}
	debugLog("[MOCK-NVML] nvmlInternalGetExportTable returning SUCCESS with table at %p\n", &internalExportTable[0])
	return C.NVML_SUCCESS
}
