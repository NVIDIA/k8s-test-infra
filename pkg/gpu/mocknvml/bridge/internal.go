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
// up to `capacity` entries for the device into `buf` and returns the number
// written. The entry layout is documented on the Go side.
extern unsigned int mockInternalFillProcessList(void* handle, void* buf, unsigned int capacity);

// Forward declaration of the host-side max PCIe link generation lookup (defined
// below in Go). Returns 0 when the device is unknown or configures no PCIe block.
extern unsigned int mockInternalHostMaxPcieLinkGen(void* handle);

// Forward declaration of the sparse-operation-mode architecture gate (defined
// below in Go). Returns 1 when the device's architecture reports the mode.
extern int mockInternalReportsSparseOperationMode(void* handle);

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

// Known slots of the export table nvidia-smi fetches for GUID
// 6c3efec4-8fc9-4e6c-a327-ee696e12f7c4, as table index (byte offset / 8). The
// slot is part of that table's ABI, so it -- not the shape of the arguments --
// is what identifies the internal entry point being called. Recovered by giving
// every slot its own trampoline and logging which slots each nvidia-smi view
// hits (580.65.06).
#define MOCK_SLOT_DEVICE_HANDLE_BY_INDEX 81
#define MOCK_SLOT_PROCESS_LIST_FIRST 213
#define MOCK_SLOT_PROCESS_LIST_LAST 215
#define MOCK_SLOT_HOST_MAX_PCIE_LINK_GEN 230
#define MOCK_SLOT_SPARSE_OPERATION_MODE 249

// C stub function for internal export table
// This gets called by nvidia-smi via the export table function pointers
static nvmlReturn_t internalStubFunction(unsigned int slot, void* arg0, void* arg1, void* arg2, void* arg3) {
    (void)arg3;
    uintptr_t rawArg0 = (uintptr_t)arg0;
    uintptr_t rawArg2 = (uintptr_t)arg2;

    // Device index lookup: fn(unsigned int index, nvmlDevice_t* out, 0x22, ...).
    if (slot == MOCK_SLOT_DEVICE_HANDLE_BY_INDEX && rawArg0 < 32 && rawArg2 == 0x22 && arg1 != NULL) {
        nvmlDevice_t* outputPtr = (nvmlDevice_t*)arg1;
        nvmlReturn_t ret = nvmlDeviceGetHandleByIndex_v2((unsigned int)rawArg0, outputPtr);
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u DeviceGetHandle(%u) -> ret=%d, handle=%p\n",
                    slot, (unsigned int)rawArg0, ret, (void*)outputPtr->handle);
        }
        return ret;
    }

    if (arg1 == NULL || !mockInternalIsDeviceHandle(arg0)) {
        // Non-device call - return SUCCESS to acknowledge.
        //
        // Slots with no implementation land here, so it must stay SUCCESS:
        // `nvidia-smi topo -m` probes internal calls before building the matrix
        // and aborts with "Failed to run topology matrix" on any error.
        return NVML_SUCCESS;
    }

    // Process list: fn(nvmlDevice_t device, unsigned int* count, entry* array).
    // nvidia-smi enumerates running processes for the default table's Processes
    // box through these three internal slots rather than the public
    // nvmlDeviceGet*RunningProcesses APIs. arg1 points to the caller's buffer
    // capacity (pre-filled, observed 250/500) and arg2 to the array itself. The
    // caller expects the count to be overwritten with the real number of
    // entries; leaving it untouched is what made nvidia-smi render its whole
    // uninitialized buffer as phantom processes (PID 0, empty name, 0 MiB). The
    // entry layout is documented on mockInternalFillProcessList.
    //
    // Restricting the write to these slots is load-bearing. Other per-device
    // slots take fewer arguments -- slot 230 takes two, leaving arg2 holding a
    // stale register value that points into this library -- so deciding from the
    // argument shape alone mistook them for a process list and wrote entries
    // over whatever arg2 happened to point at, faulting in `nvidia-smi -q` as
    // soon as a process was configured.
    if (slot >= MOCK_SLOT_PROCESS_LIST_FIRST && slot <= MOCK_SLOT_PROCESS_LIST_LAST &&
        rawArg2 > 0x100000) {
        unsigned int cap = *(unsigned int*)arg1;
        if (cap >= 1 && cap <= 65536) {
            unsigned int n = mockInternalFillProcessList(arg0, arg2, cap);
            *(unsigned int*)arg1 = n;
            if (isDebugEnabled()) {
                fprintf(stderr, "[C-STUB] slot %u process list (handle=%p) -> %u entries\n",
                        slot, arg0, n);
            }
            return NVML_SUCCESS;
        }
    }

    // Host-side max PCIe link generation: fn(nvmlDevice_t device, unsigned int* out).
    // nvidia-smi's "Host Max" row comes from here, not from any public NVML API
    // -- none exposes a host-side maximum. Falling through to the zero write
    // below is what made every profile report an impossible Gen0 (issue #638).
    // Slot located by its position in the -q call sequence: it fires between
    // nvmlDeviceGetGpuMaxPcieLinkGeneration ("Device Max") and
    // nvmlDeviceGetMaxPcieLinkWidth, and writing a marker value here moves the
    // Host Max row of `-q`, the <max_host_link_gen> element of `-q -x` and the
    // pcie.link.gen.hostmax query field together.
    if (slot == MOCK_SLOT_HOST_MAX_PCIE_LINK_GEN) {
        unsigned int gen = mockInternalHostMaxPcieLinkGen(arg0);
        // Leave an unconfigured profile on the zero-count path rather than
        // inventing a generation for it.
        if (gen > 0) {
            *(unsigned int*)arg1 = gen;
            if (isDebugEnabled()) {
                fprintf(stderr, "[C-STUB] slot %u host max PCIe link gen (handle=%p) -> %u\n",
                        slot, arg0, gen);
            }
            return NVML_SUCCESS;
        }
    }

    // Sparse Operation Mode: fn(nvmlDevice_t device, unsigned int* out).
    // nvidia-smi's "Sparse Operation Mode" row comes from here -- no public
    // NVML entry point exposes it, which is why a repository-wide search for
    // the name finds nothing. Falling through to the zero write below is what
    // made every profile, Blackwell included, report "Disabled" (issue #679);
    // real Blackwell has no such mode and reports N/A, which is what an error
    // from this slot renders as. Slot located by its position in the -q call
    // sequence: it fires between the clocks-event-reason field values and
    // nvmlDeviceGetMemoryInfo_v2, exactly where the row sits in the output.
    if (slot == MOCK_SLOT_SPARSE_OPERATION_MODE &&
        !mockInternalReportsSparseOperationMode(arg0)) {
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u sparse operation mode (handle=%p) -> NOT_SUPPORTED\n",
                    slot, arg0);
        }
        return NVML_ERROR_NOT_SUPPORTED;
    }

    // Every other per-device call still gets an explicit zero count. Leaving it
    // at the caller's pre-filled capacity makes nvidia-smi walk an array we
    // never wrote: that is the phantom-process bug for the list views, and a
    // SIGSEGV in `pmon`.
    *(unsigned int*)arg1 = 0;
    if (isDebugEnabled()) {
        fprintf(stderr, "[C-STUB] slot %u device list query (handle=%p) -> 0 entries\n", slot, arg0);
    }
    return NVML_SUCCESS;
}

// One trampoline per export-table slot, so the dispatcher above is told which
// internal entry point nvidia-smi called instead of having to guess from the
// argument shape. The 16x16 expansion is only a way to write 256 distinct
// function bodies; slot number is hi*16 + lo, matching the table index.
#define MOCK_SLOT_STUB(hi, lo) \
    static nvmlReturn_t mockSlotStub_##hi##_##lo(void* a0, void* a1, void* a2, void* a3) { \
        return internalStubFunction(hi * 16 + lo, a0, a1, a2, a3); \
    }
#define MOCK_SLOT_ADDR(hi, lo) (void*)mockSlotStub_##hi##_##lo,

#define MOCK_SLOT_ROW(M, hi) \
    M(hi, 0) M(hi, 1) M(hi, 2) M(hi, 3) M(hi, 4) M(hi, 5) M(hi, 6) M(hi, 7) \
    M(hi, 8) M(hi, 9) M(hi, 10) M(hi, 11) M(hi, 12) M(hi, 13) M(hi, 14) M(hi, 15)
#define MOCK_SLOT_ALL(M) \
    MOCK_SLOT_ROW(M, 0) MOCK_SLOT_ROW(M, 1) MOCK_SLOT_ROW(M, 2) MOCK_SLOT_ROW(M, 3) \
    MOCK_SLOT_ROW(M, 4) MOCK_SLOT_ROW(M, 5) MOCK_SLOT_ROW(M, 6) MOCK_SLOT_ROW(M, 7) \
    MOCK_SLOT_ROW(M, 8) MOCK_SLOT_ROW(M, 9) MOCK_SLOT_ROW(M, 10) MOCK_SLOT_ROW(M, 11) \
    MOCK_SLOT_ROW(M, 12) MOCK_SLOT_ROW(M, 13) MOCK_SLOT_ROW(M, 14) MOCK_SLOT_ROW(M, 15)

MOCK_SLOT_ALL(MOCK_SLOT_STUB)

static void* mockSlotStubs[256] = { MOCK_SLOT_ALL(MOCK_SLOT_ADDR) };

// getSlotStubAddress returns the trampoline that identifies table slot.
static void* getSlotStubAddress(unsigned int slot) {
    return mockSlotStubs[slot];
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

// mockInternalHostMaxPcieLinkGen returns the host-side maximum PCIe link
// generation for the device nvidia-smi passed through the internal export
// table, or 0 when the handle is unknown or the profile configures no PCIe
// block. This is the only path to nvidia-smi's "Host Max" row; the semantics of
// the value live on engine.ConfigurableDevice.HostMaxPcieLinkGeneration.
//
//export mockInternalHostMaxPcieLinkGen
func mockInternalHostMaxPcieLinkGen(handle unsafe.Pointer) C.uint {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return 0
	}
	return C.uint(dev.HostMaxPcieLinkGeneration())
}

// mockInternalReportsSparseOperationMode reports whether the device nvidia-smi
// passed through the internal export table answers the Sparse Operation Mode
// query. An unknown handle reports 1 so an unrecognised device keeps the
// zero-count fall-through rather than turning into an error.
//
//export mockInternalReportsSparseOperationMode
func mockInternalReportsSparseOperationMode(handle unsafe.Pointer) C.int {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil || dev.ReportsSparseOperationMode() {
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
		p, _ := dev.ProcessByPID(all[i].Pid)
		writeProcessEntry(buf, i, all[i].Pid, all[i].UsedGpuMemory, p.Name)
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
var (
	internalExportTable    [256]uintptr
	exportTableInitialized = false
)

func initExportTable() {
	if exportTableInitialized {
		return
	}
	// Set up the export table
	// Offset 0: size field (must be > 648 = 0x288)
	internalExportTable[0] = 0x1000

	// Every slot gets its own trampoline so a call can be attributed to the
	// internal entry point nvidia-smi asked for.
	for i := 1; i < 256; i++ {
		internalExportTable[i] = uintptr(C.getSlotStubAddress(C.uint(i)))
	}

	exportTableInitialized = true
	debugLog("[MOCK-NVML] Export table initialized with %d per-slot trampolines\n", 255)
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
