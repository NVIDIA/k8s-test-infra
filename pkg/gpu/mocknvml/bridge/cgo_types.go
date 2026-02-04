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

// Package main provides the shared CGo type definitions for the mock NVML bridge.
// This file contains the CGo preamble with C type definitions that are shared
// across all files in the bridge package.
//
// NOTE: Each .go file that uses CGo types must include its own CGo preamble
// since Go does not share CGo preambles between files. However, this file
// serves as the canonical reference for the type definitions.
package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
// We use a custom types-only header (not full nvml.h) to avoid conflicts
// between nvml.h function declarations and CGo-generated exports.
// These types match vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h exactly.
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

// This file only contains the CGo preamble and type definitions.
// The actual imports and usage happen in other files (helpers.go, init.go, etc.)
// that include the same CGo preamble for type compatibility.
