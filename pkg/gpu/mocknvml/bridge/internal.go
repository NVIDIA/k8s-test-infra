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
// below in Go). It returns NOT_SUPPORTED when the profile omits the host value.
extern int mockInternalHostMaxPcieLinkGen(void* handle, unsigned int* value);

// Forward declarations of the vGPU lookups (defined below in Go). Both return 1
// when the device is the kind the slot asks about, 0 otherwise.
extern int mockInternalIsVgpuHost(void* handle);
extern int mockInternalGuestLicenseState(void* handle, unsigned int* state);

// Forward declaration of the public persistence-mode setter (defined in device.go).
extern nvmlReturn_t nvmlDeviceSetPersistenceMode(nvmlDevice_t device, nvmlEnableState_t mode);

// Forward declaration of the GPU reset (defined in gpu_reset.go). Returns 1 when
// the device returned to its healthy baseline, 0 when it could not.
extern int mockInternalResetGPU(void* handle);

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
#define MOCK_SLOT_GPU_RESET 20
#define MOCK_SLOT_VGPU_HOST 72
#define MOCK_SLOT_DEVICE_HANDLE_BY_INDEX 81
#define MOCK_SLOT_GUEST_LICENSE_STATE 189
#define MOCK_SLOT_GPU_RESET_COMPLETE 197
#define MOCK_SLOT_PROCESS_LIST_FIRST 213
#define MOCK_SLOT_PROCESS_LIST_LAST 215
#define MOCK_SLOT_HOST_MAX_PCIE_LINK_GEN 230
#define MOCK_SLOT_SET_PERSISTENCE_MODE 251

// The table holds MOCK_EXPORT_TABLE_SLOTS words: the size word, then one
// trampoline per slot. The 580.65.06 table ends at slot 255; a 615 nvidia-smi
// reads further, so the table must be as long as its size word says.
#define MOCK_EXPORT_TABLE_SLOTS 512
#define MOCK_SLOT_580_TABLE_END 256

// Entry points a 615 nvidia-smi calls past the end of the 580.65.06 table,
// recovered the same way from 615.71.09. Every command that changes GPU state
// counts unlisted GPUs first (270); `--gpu-reset` then allocates a group of the
// GPUs it took through slots 20 and 197 (272), runs it (273) and frees it
// through slot 184.
#define MOCK_SLOT_UNLISTED_GPU_COUNT 270
#define MOCK_SLOT_RESET_GROUP_ALLOC 272
#define MOCK_SLOT_RESET_GROUP_RUN 273

// The versioned parameter blocks those entries take, tagged the way NVML tags
// its versioned structs: version in the top byte, size in bytes below it.
#define MOCK_UNLISTED_GPU_COUNT_V1 0x01000008u // { version, count }
#define MOCK_RESET_GROUP_RUN_V1 0x01000030u    // { version, pad, group, buf, buf, scope*, word }

// The words of the run block, counted in pointers. Of everything the block
// reaches, a 615 nvidia-smi reads back one word after a successful run: the
// first behind MOCK_RESET_GROUP_RUN_SCOPE, which, nonzero, makes it report that
// the reset took the NVSwitches along with the GPUs. Poisoning each word of the
// two buffers before it changes nothing nvidia-smi prints, so those stay its own.
#define MOCK_RESET_GROUP_RUN_GROUP 1
#define MOCK_RESET_GROUP_RUN_SCOPE 4

// The group slot 272 hands out. The reset itself happened at slot 20, so the
// group holds nothing; its address is only a handle nvidia-smi passes back.
static unsigned char mockResetGroup;

// C stub function for internal export table
// This gets called by nvidia-smi via the export table function pointers
static nvmlReturn_t internalStubFunction(unsigned int slot, void* arg0, void* arg1, void* arg2, void* arg3) {
    uintptr_t rawArg0 = (uintptr_t)arg0;
    uintptr_t rawArg2 = (uintptr_t)arg2;

    // Unlisted GPUs are GPUs the driver manages but does not enumerate:
    // fn({ version, count }*). A 615 nvidia-smi stops at any error here, so
    // the mock reports none, as a node without them does.
    if (slot == MOCK_SLOT_UNLISTED_GPU_COUNT) {
        unsigned int* block = (unsigned int*)arg0;
        if (block == NULL || block[0] != MOCK_UNLISTED_GPU_COUNT_V1) {
            if (isDebugEnabled()) {
                fprintf(stderr, "[C-STUB] slot %u unlisted GPU count: unknown block %p -> VERSION_MISMATCH\n",
                        slot, arg0);
            }
            return NVML_ERROR_ARGUMENT_VERSION_MISMATCH;
        }
        block[1] = 0;
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u unlisted GPU count -> 0\n", slot);
        }
        return NVML_SUCCESS;
    }

    // Reset group: fn(group* out, nvmlPciInfo_t* gpus, unsigned int count,
    // flags), then fn({ version, pad, group, buf, buf, scope*, word }*) to run
    // it. A successful run is every GPU in the group reset; the mock resets no
    // NVSwitch, so it writes the scope word 0.
    if (slot == MOCK_SLOT_RESET_GROUP_ALLOC) {
        if (arg0 == NULL || arg1 == NULL || rawArg2 == 0) {
            return NVML_ERROR_INVALID_ARGUMENT;
        }
        *(void**)arg0 = &mockResetGroup;
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u reset group of %lu GPUs -> %p\n",
                    slot, (unsigned long)rawArg2, (void*)&mockResetGroup);
        }
        return NVML_SUCCESS;
    }
    if (slot == MOCK_SLOT_RESET_GROUP_RUN) {
        unsigned int* block = (unsigned int*)arg0;
        if (block == NULL || block[0] != MOCK_RESET_GROUP_RUN_V1) {
            return NVML_ERROR_ARGUMENT_VERSION_MISMATCH;
        }
        void** words = (void**)arg0;
        if (words[MOCK_RESET_GROUP_RUN_GROUP] != (void*)&mockResetGroup) {
            return NVML_ERROR_INVALID_ARGUMENT;
        }
        unsigned int* scope = (unsigned int*)words[MOCK_RESET_GROUP_RUN_SCOPE];
        if (scope != NULL) {
            scope[0] = 0;
        }
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u reset group run -> SUCCESS\n", slot);
        }
        return NVML_SUCCESS;
    }

    // An entry point of a newer nvidia-smi that nothing here answers. Declining
    // is safer than the SUCCESS the catch-all below gives older slots: a newer
    // caller trusts its out-parameters on SUCCESS, which is how slot 270 once
    // handed back stack garbage as a GPU count.
    if (slot >= MOCK_SLOT_580_TABLE_END) {
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u not implemented (arg0=%p, arg1=%p, arg2=%p, arg3=%p) -> NOT_SUPPORTED\n",
                    slot, arg0, arg1, arg2, arg3);
        }
        return NVML_ERROR_NOT_SUPPORTED;
    }

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

    // GPU reset. `nvidia-smi --gpu-reset` / `-r` performs the whole reset through
    // these two slots and nothing else reaches either of them, so serving them
    // costs no other view. nvidia-smi calls slot 20 once per GPU being reset and
    // slot 197 twice, then prints "GPU <busid> was successfully reset."
    //
    // Both must return before the zero write at the bottom of this function. That
    // write assumes arg1 is a caller-supplied count, and on slot 197 it is not:
    // the write faulted there, which is what made every reset die with a SIGSEGV
    // and no diagnostic (the fault landed ahead of the debug print, so the log
    // stopped mid-sequence and pointed at nvidia-smi rather than at us).
    //
    // The device-handle gate matters for what an error here means. Without it a
    // slot 20 call carrying anything other than a registered handle -- a probe,
    // or an engine still at initCount 0 -- fails the lookup and reports
    // NVML_ERROR_OPERATING_SYSTEM, the one answer the catch-all below documents
    // as unsafe for a call that was never a device call. Falling through to that
    // guard instead keeps NVML_ERROR_OPERATING_SYSTEM meaning "this device's
    // reset was attempted and failed".
    if (slot == MOCK_SLOT_GPU_RESET && mockInternalIsDeviceHandle(arg0)) {
        // The reset the mock can actually perform is clearing the device's
        // injected overrides, the same mutation `nvml-mock-ctl reset --gpu <n>`
        // makes. Real hardware clears its transient error state here, so a
        // profile returning to its healthy baseline is the faithful analogue.
        int ok = mockInternalResetGPU(arg0);
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u GPU reset (handle=%p) -> ok=%d\n", slot, arg0, ok);
        }
        // An error here is what nvidia-smi renders as "GPU Reset couldn't run",
        // which is the honest answer when the overrides could not be cleared.
        return ok ? NVML_SUCCESS : NVML_ERROR_OPERATING_SYSTEM;
    }
    if (slot == MOCK_SLOT_GPU_RESET_COMPLETE) {
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u GPU reset completion (handle=%p)\n", slot, arg0);
        }
        return NVML_SUCCESS;
    }

    // Persistence mode: fn(nvmlDevice_t device, nvmlEnableState_t mode).
    // `nvidia-smi -pm` sets it here and never calls the public
    // nvmlDeviceSetPersistenceMode, and only when the mode would change. Both
    // catch-alls below mishandle it: DISABLED is 0, so it reads as a NULL arg1
    // and is acknowledged without being recorded, and ENABLED would be taken
    // for a count pointer and written through.
    if (slot == MOCK_SLOT_SET_PERSISTENCE_MODE && mockInternalIsDeviceHandle(arg0)) {
        nvmlDevice_t device = { .handle = (struct nvmlDevice_st*)arg0 };
        nvmlReturn_t ret = nvmlDeviceSetPersistenceMode(device, (nvmlEnableState_t)(uintptr_t)arg1);
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u SetPersistenceMode(handle=%p, mode=%u) -> ret=%d\n",
                    slot, arg0, (unsigned int)(uintptr_t)arg1, ret);
        }
        return ret;
    }

    // Host-side max PCIe link generation: fn(nvmlDevice_t device, unsigned int* out).
    // Slot located by its position in the -q call sequence: it fires between
    // nvmlDeviceGetGpuMaxPcieLinkGeneration ("Device Max") and
    // nvmlDeviceGetMaxPcieLinkWidth. Writing a marker here moves the Host Max
    // row of `-q`, <max_host_link_gen> in `-q -x` and pcie.link.gen.hostmax
    // together; no public NVML API exposes this host-side maximum.
    // Handle this before the generic device-handle guard: the historical slot
    // response for an unknown handle is success with a zero output, whereas a
    // known device without a configured host maximum reports NOT_SUPPORTED.
    if (slot == MOCK_SLOT_HOST_MAX_PCIE_LINK_GEN && arg1 != NULL) {
        unsigned int gen = 0;
        nvmlReturn_t ret = mockInternalHostMaxPcieLinkGen(arg0, &gen);
        if (ret != NVML_SUCCESS) {
            return ret;
        }
        *(unsigned int*)arg1 = gen;
        if (gen > 0 && isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u host max PCIe link gen (handle=%p) -> %u\n",
                    slot, arg0, gen);
        }
        return NVML_SUCCESS;
    }

    if (arg1 == NULL || !mockInternalIsDeviceHandle(arg0)) {
        // Non-device call - return SUCCESS to acknowledge.
        //
        // Slots with no implementation land here, so it must stay SUCCESS:
        // `nvidia-smi topo -m` probes internal calls before building the matrix
        // and aborts with "Failed to run topology matrix" on any error.
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u unhandled (arg0=%p, arg1=%p, arg2=%p) -> SUCCESS\n",
                    slot, arg0, arg1, arg2);
        }
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

    // vGPU manager presence: fn(nvmlDevice_t device, unsigned int* out).
    // `nvidia-smi vgpu` asks this before any public vGPU call and prints "No
    // supported devices in vGPU mode" while it answers 0. `pmon` asks it too,
    // and declines a host GPU the way it does on a hypervisor.
    if (slot == MOCK_SLOT_VGPU_HOST && mockInternalIsVgpuHost(arg0)) {
        *(unsigned int*)arg1 = 1;
        if (isDebugEnabled()) {
            fprintf(stderr, "[C-STUB] slot %u vGPU host (handle=%p) -> 1\n", slot, arg0);
        }
        return NVML_SUCCESS;
    }

    // Guest license state: fn(nvmlDevice_t device, unsigned int* out), in
    // nvmlGridLicenseState_t terms. `nvidia-smi -q` renders a guest's License
    // Status from this, not from nvmlDeviceGetGridLicensableFeatures.
    if (slot == MOCK_SLOT_GUEST_LICENSE_STATE) {
        unsigned int state = 0;
        if (mockInternalGuestLicenseState(arg0, &state)) {
            *(unsigned int*)arg1 = state;
            if (isDebugEnabled()) {
                fprintf(stderr, "[C-STUB] slot %u guest license state (handle=%p) -> %u\n",
                        slot, arg0, state);
            }
            return NVML_SUCCESS;
        }
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
// argument shape. The 32x16 expansion is only a way to write 512 distinct
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
    MOCK_SLOT_ROW(M, 12) MOCK_SLOT_ROW(M, 13) MOCK_SLOT_ROW(M, 14) MOCK_SLOT_ROW(M, 15) \
    MOCK_SLOT_ROW(M, 16) MOCK_SLOT_ROW(M, 17) MOCK_SLOT_ROW(M, 18) MOCK_SLOT_ROW(M, 19) \
    MOCK_SLOT_ROW(M, 20) MOCK_SLOT_ROW(M, 21) MOCK_SLOT_ROW(M, 22) MOCK_SLOT_ROW(M, 23) \
    MOCK_SLOT_ROW(M, 24) MOCK_SLOT_ROW(M, 25) MOCK_SLOT_ROW(M, 26) MOCK_SLOT_ROW(M, 27) \
    MOCK_SLOT_ROW(M, 28) MOCK_SLOT_ROW(M, 29) MOCK_SLOT_ROW(M, 30) MOCK_SLOT_ROW(M, 31)

MOCK_SLOT_ALL(MOCK_SLOT_STUB)

static void* mockSlotStubs[MOCK_EXPORT_TABLE_SLOTS] = { MOCK_SLOT_ALL(MOCK_SLOT_ADDR) };

// getSlotStubAddress returns the trampoline that identifies table slot.
static void* getSlotStubAddress(unsigned int slot) {
    return mockSlotStubs[slot];
}

// callSlotForTest makes one export-table call the way nvidia-smi does. arg1
// goes by value because some slots take an integer there, not a pointer.
static nvmlReturn_t callSlotForTest(unsigned int slot, void* arg0, uintptr_t arg1) {
    return internalStubFunction(slot, arg0, (void*)arg1, NULL, NULL);
}

static nvmlReturn_t hostMaxPcieLinkGenForTest(void* handle, unsigned int* value) {
    return internalStubFunction(MOCK_SLOT_HOST_MAX_PCIE_LINK_GEN, handle, value, NULL, NULL);
}

// callSlot4ForTest makes an export-table call that takes all four arguments.
static nvmlReturn_t callSlot4ForTest(unsigned int slot, void* arg0, void* arg1, uintptr_t arg2, uintptr_t arg3) {
    return internalStubFunction(slot, arg0, arg1, (void*)arg2, (void*)arg3);
}
*/
import "C"

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const persistenceModeSlotForTest = C.MOCK_SLOT_SET_PERSISTENCE_MODE

const (
	vgpuHostSlotForTest          = C.MOCK_SLOT_VGPU_HOST
	guestLicenseStateSlotForTest = C.MOCK_SLOT_GUEST_LICENSE_STATE
)

// internalSlotReadForTest makes one export-table call whose arg1 is an
// unsigned int out-parameter and returns what the slot wrote there. The
// buffer is C memory so the call passes no Go pointer.
func internalSlotReadForTest(slot uint, handle unsafe.Pointer) (uint32, nvml.Return) {
	out := (*C.uint)(C.malloc(C.size_t(unsafe.Sizeof(C.uint(0)))))
	defer C.free(unsafe.Pointer(out))
	*out = 0xdeadbeef
	ret := nvml.Return(C.callSlotForTest(C.uint(slot), handle, C.uintptr_t(uintptr(unsafe.Pointer(out)))))
	return uint32(*out), ret
}

// internalSlotCallForTest dispatches one export-table call; test files cannot
// use cgo.
func internalSlotCallForTest(slot uint, handle unsafe.Pointer, arg1 uintptr) nvml.Return {
	return nvml.Return(C.callSlotForTest(C.uint(slot), handle, C.uintptr_t(arg1)))
}

func hostMaxPcieLinkGenForTest(handle unsafe.Pointer, initial uint32) (uint32, nvml.Return) {
	value := C.uint(initial)
	ret := C.hostMaxPcieLinkGenForTest(handle, &value)
	return uint32(value), nvml.Return(ret)
}

func hostMaxPcieLinkGenLookupForTest(handle unsafe.Pointer, initial uint32) (uint32, nvml.Return) {
	value := C.uint(initial)
	ret := mockInternalHostMaxPcieLinkGen(handle, &value)
	return uint32(value), nvml.Return(ret)
}

// internalSlotBlockCallForTest makes an export-table call whose first argument
// is a parameter block, the way the 615 entry points take theirs, and returns
// the block as the slot left it. arg1 to arg3 go through unchanged; a non-zero
// arg1Words also passes a second block of that many zeroed words as arg1. The
// blocks are C memory so the call passes no Go pointer.
func internalSlotBlockCallForTest(slot uint, block []uint64, arg1Words int, arg2, arg3 uintptr) ([]uint64, nvml.Return) {
	word := C.size_t(unsafe.Sizeof(uint64(0)))
	buf := (*uint64)(C.calloc(C.size_t(len(block)), word))
	defer C.free(unsafe.Pointer(buf))
	view := unsafe.Slice(buf, len(block))
	copy(view, block)
	var arg1 unsafe.Pointer
	if arg1Words > 0 {
		arg1 = C.calloc(C.size_t(arg1Words), word)
		defer C.free(arg1)
	}
	ret := nvml.Return(C.callSlot4ForTest(C.uint(slot), unsafe.Pointer(buf), arg1, C.uintptr_t(arg2), C.uintptr_t(arg3)))
	return slices.Clone(view), ret
}

// resetGroupRunForTest runs the reset group as a 615 nvidia-smi does, with
// the scope word nvidia-smi reads back prefilled, and returns that word as the
// run left it.
func resetGroupRunForTest(version uint32, group uint64, scopePrefill uint32) (uint32, nvml.Return) {
	scope := (*C.uint)(C.calloc(2, C.size_t(unsafe.Sizeof(C.uint(0)))))
	defer C.free(unsafe.Pointer(scope))
	*scope = C.uint(scopePrefill)
	block := make([]uint64, (C.MOCK_RESET_GROUP_RUN_V1&0xffffff)/8)
	block[0] = uint64(version)
	block[C.MOCK_RESET_GROUP_RUN_GROUP] = group
	block[C.MOCK_RESET_GROUP_RUN_SCOPE] = uint64(uintptr(unsafe.Pointer(scope)))
	_, ret := internalSlotBlockCallForTest(C.MOCK_SLOT_RESET_GROUP_RUN, block, 0, 0, 0)
	return uint32(*scope), ret
}

// The 615 entry points and their parameter-block tags, for the tests.
const (
	unlistedGPUCountSlotForTest = C.MOCK_SLOT_UNLISTED_GPU_COUNT
	resetGroupAllocSlotForTest  = C.MOCK_SLOT_RESET_GROUP_ALLOC
	unlistedGPUCountV1ForTest   = C.MOCK_UNLISTED_GPU_COUNT_V1
	resetGroupRunV1ForTest      = C.MOCK_RESET_GROUP_RUN_V1
	firstSlotPast580ForTest     = C.MOCK_SLOT_580_TABLE_END
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
// table, or NOT_SUPPORTED when the profile omits the host maximum. An unknown
// handle retains the historical success-with-zero result. This is the only
// path to nvidia-smi's "Host Max" row; the semantics of
// the value live on engine.ConfigurableDevice.HostMaxPcieLinkGeneration.
//
//export mockInternalHostMaxPcieLinkGen
func mockInternalHostMaxPcieLinkGen(handle unsafe.Pointer, value *C.uint) C.int {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		*value = 0
		return C.NVML_SUCCESS
	}
	gen := dev.HostMaxPcieLinkGeneration()
	if gen == 0 {
		return C.NVML_ERROR_NOT_SUPPORTED
	}
	*value = C.uint(gen)
	return C.NVML_SUCCESS
}

// mockInternalIsVgpuHost reports whether the GPU runs the vGPU manager.
//
//export mockInternalIsVgpuHost
func mockInternalIsVgpuHost(handle unsafe.Pointer) C.int {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil || !dev.IsVgpuHost() {
		return 0
	}
	return 1
}

// mockInternalGuestLicenseState writes a vGPU guest's license state and
// reports whether the GPU is one.
//
//export mockInternalGuestLicenseState
func mockInternalGuestLicenseState(handle unsafe.Pointer, state *C.uint) C.int {
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return 0
	}
	s, ok := dev.VgpuGuestLicenseState()
	if !ok {
		return 0
	}
	*state = C.uint(s)
	return 1
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
	// The three lists fail only for a lost GPU, and then return no entries,
	// which is the list nvidia-smi should show for a GPU it cannot reach.
	compute, _ := dev.GetComputeRunningProcesses()
	graphics, _ := dev.GetGraphicsRunningProcesses()
	mps, _ := dev.GetMPSComputeRunningProcesses()
	all := slices.Concat(compute, graphics, mps)

	n := len(all)
	if n > int(capacity) {
		n = int(capacity)
	}
	for i := range n {
		p, _ := dev.ProcessByPID(all[i].Pid)
		writeProcessEntry(buf, i, all[i].Pid, all[i].UsedGpuMemory, p.Name)
	}
	return C.uint(n)
}

// writeProcessEntry writes one process into the caller's array at index, using
// the layout documented above. The name is truncated to fit and always
// NUL-terminated.
// nvmlNoMIGInstanceID is the gpuInstanceId / computeInstanceId value NVML
// reports for anything not attributed to a MIG instance: process entries and
// nvmlEventData_t alike ("0xFFFFFFFF otherwise" in the NVML docs).
const nvmlNoMIGInstanceID uint32 = 0xFFFFFFFF

func writeProcessEntry(buf unsafe.Pointer, index int, pid uint32, usedGpuMemory uint64, name string) {
	e := unsafe.Add(buf, index*procEntrySize)
	*(*uint32)(e) = pid
	*(*uint64)(unsafe.Add(e, 8)) = usedGpuMemory
	*(*uint32)(unsafe.Add(e, 16)) = nvmlNoMIGInstanceID // gpuInstanceId = N/A (non-MIG)
	*(*uint32)(unsafe.Add(e, 20)) = nvmlNoMIGInstanceID // computeInstanceId = N/A
	*(*uint64)(unsafe.Add(e, 24)) = 0                   // usedGpuCcProtectedMemory

	if len(name) > procEntryNameMax-1 {
		name = name[:procEntryNameMax-1]
	}
	nameBuf := unsafe.Slice((*byte)(unsafe.Add(e, procEntryNameOffset)), procEntryNameMax)
	copy(nameBuf, name)
	nameBuf[len(name)] = 0
}

// Internal export table for nvidia-smi compatibility.
// Based on reverse engineering: table[0] is the table's size in bytes, and
// each later word is a function pointer (table[648/8] is the device handle
// lookup).
//
// The size word is what a caller bounds its reads with, so the array must be
// exactly that long. A 615 nvidia-smi reads past slot 255; when the table
// claimed 4 KiB but held 2, it took whatever word followed the array for a
// function pointer and crashed `nvidia-smi -q`.
const exportTableSlots = C.MOCK_EXPORT_TABLE_SLOTS

var (
	internalExportTable    [exportTableSlots]uintptr
	exportTableInitialized = false
)

func initExportTable() {
	if exportTableInitialized {
		return
	}
	internalExportTable[0] = uintptr(len(internalExportTable)) * unsafe.Sizeof(internalExportTable[0])

	// Every slot gets its own trampoline so a call can be attributed to the
	// internal entry point nvidia-smi asked for.
	for i := 1; i < len(internalExportTable); i++ {
		internalExportTable[i] = uintptr(C.getSlotStubAddress(C.uint(i)))
	}

	exportTableInitialized = true
	debugLog("[MOCK-NVML] Export table initialized with %d per-slot trampolines\n", len(internalExportTable)-1)
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
