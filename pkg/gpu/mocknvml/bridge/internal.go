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

// Forward declaration of our device-handle validator (defined below in Go).
// Returns 1 if the raw handle value belongs to a registered mock device.
extern int mockInternalIsDeviceHandle(void* handle);

// Forward declaration of the process-list filler (defined below in Go). Writes
// up to `capacity` nvmlProcessInfo_t entries for the device into `buf` and
// returns the number written.
extern unsigned int mockInternalFillProcessList(void* handle, void* buf, unsigned int capacity);

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

    // Process list: fn(nvmlDevice_t device, unsigned int* count, entry* array).
    // nvidia-smi enumerates running processes for the default table, `-q` and
    // `--query-compute-apps` through this internal entry point rather than the
    // public nvmlDeviceGet*RunningProcesses APIs. arg0 is a device handle we
    // handed out, arg1 points to the caller's buffer capacity (pre-filled, e.g.
    // 250/500) and arg2 to the array itself. The caller expects the count to be
    // overwritten with the real number of entries; leaving it untouched is what
    // made nvidia-smi render its whole uninitialized buffer as phantom
    // processes (PID 0, empty name, 0 MiB).
    //
    // Other per-device internal calls reuse this same stub with a garbage arg2
    // (observed 0x1 / 0x14 / 0x50) and only get the count zeroed. The entry
    // layout is documented on mockInternalFillProcessList.
    if (arg1 != NULL && mockInternalIsDeviceHandle(arg0)) {
        if (rawArg2 > 0x100000) {
            unsigned int cap = *(unsigned int*)arg1;
            if (cap >= 1 && cap <= 65536) {
                unsigned int n = mockInternalFillProcessList(arg0, arg2, cap);
                *(unsigned int*)arg1 = n;
                if (isDebugEnabled()) {
                    fprintf(stderr, "[C-STUB] process list (handle=%p) -> %u entries\n", arg0, n);
                }
                return NVML_SUCCESS;
            }
        }
        // Every other per-device call still gets an explicit zero count. Leaving
        // it at the caller's pre-filled capacity makes nvidia-smi walk an array
        // we never wrote: that is the phantom-process bug for the list views,
        // and a SIGSEGV in `pmon`.
        *(unsigned int*)arg1 = 0;
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] device list query (handle=%p) -> 0 entries\n", arg0);
        }
        return NVML_SUCCESS;
    }

    // Non-device call - return SUCCESS to acknowledge.
    //
    // Every slot of the export table points at this one stub, so this arm is
    // reached by unrelated internal entry points too. It must stay SUCCESS:
    // `nvidia-smi topo -m` probes internal calls here before building the
    // matrix and aborts with "Failed to run topology matrix" on any error.
    // The phantom-process bug this file also fixes came from the per-device
    // branch above leaving the caller's count untouched, not from this return.
    if (isDebugEnabled()) {
        fprintf(stderr, "[C-STUB] non-device internal call -> SUCCESS\n");
    }
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

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// mockInternalIsDeviceHandle reports whether the raw handle value passed by
// nvidia-smi through the internal export table belongs to a registered mock
// device. The internal process-enumeration call is per-device, so a valid
// handle is how the C stub distinguishes it from unrelated internal calls.
//
//export mockInternalIsDeviceHandle
func mockInternalIsDeviceHandle(handle unsafe.Pointer) C.int {
	if engine.GetEngine().LookupConfigurableDevice(handle) != nil {
		return 1
	}
	return 0
}

// Layout of one entry in the internal process-list array, recovered by probing
// the real nvidia-smi 580.65.06 (stamp each word with its own offset, then read
// back which offset each rendered row came from). The numeric header matches
// nvmlProcessDetail_v1_t -- pid@0, usedGpuMemory@8, gpuInstanceId@16,
// computeInstanceId@20, usedGpuCcProtectedMemory@24 -- but is followed by an
// inline NUL-terminated name buffer rather than a pointer, so the stride is
// 4128 and not the 24 of the public nvmlProcessInfo_t.
//
// The inline name is load-bearing, not cosmetic: without it nvidia-smi drops
// the rows from the default table's Processes box entirely and leaves Name
// blank under `-q`.
const (
	procEntryNameOffset = 32
	procEntryNameMax    = 4096
	procEntrySize       = procEntryNameOffset + procEntryNameMax
)

// mockInternalFillProcessList writes the device's configured running processes
// into the caller's array (arg2 of the internal export-table process-list call)
// and returns the number of entries written, capped at the caller's buffer
// capacity. This is what surfaces configured processes in nvidia-smi's default
// table, `-q` and `--query-compute-apps`, none of which use the public
// nvmlDeviceGet*RunningProcesses APIs.
//
// nvidia-smi labels every entry `M+C+G` in the Type column: the compute vs
// graphics distinction comes from which list the caller asked for, and this
// single entry point carries no type field (confirmed by poking every word of
// the numeric header). Configured `type:` therefore does not reach the Type
// column.
//
//export mockInternalFillProcessList
func mockInternalFillProcessList(handle unsafe.Pointer, buf unsafe.Pointer, capacity C.uint) C.uint {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil || buf == nil {
		return 0
	}
	compute, _ := dev.GetComputeRunningProcesses()
	graphics, _ := dev.GetGraphicsRunningProcesses()
	all := append(compute, graphics...)

	n := len(all)
	if n > int(capacity) {
		n = int(capacity)
	}
	for i := 0; i < n; i++ {
		writeProcessEntry(buf, i, all[i].Pid, all[i].UsedGpuMemory,
			engine.GetEngine().ProcessNameByPID(all[i].Pid))
	}
	return C.uint(n)
}

// writeProcessEntry writes one process into the caller's array at index, using
// the layout documented above. The name is truncated to fit and always
// NUL-terminated.
func writeProcessEntry(buf unsafe.Pointer, index int, pid uint32, usedGpuMemory uint64, name string) {
	e := unsafe.Add(buf, index*procEntrySize)
	*(*uint32)(e) = pid
	*(*uint64)(unsafe.Add(e, 8)) = usedGpuMemory
	*(*uint32)(unsafe.Add(e, 16)) = 0xFFFFFFFF // gpuInstanceId = N/A (non-MIG)
	*(*uint32)(unsafe.Add(e, 20)) = 0xFFFFFFFF // computeInstanceId = N/A
	*(*uint64)(unsafe.Add(e, 24)) = 0          // usedGpuCcProtectedMemory

	if len(name) > procEntryNameMax-1 {
		name = name[:procEntryNameMax-1]
	}
	nameBuf := unsafe.Slice((*byte)(unsafe.Add(e, procEntryNameOffset)), procEntryNameMax)
	copy(nameBuf, name)
	nameBuf[len(name)] = 0
}

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
