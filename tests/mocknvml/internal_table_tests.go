// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Internal export-table tests. nvidia-smi reaches several per-device queries
// through nvmlInternalGetExportTable rather than the public NVML API, so this
// surface has no go-nvml coverage and can only be driven by calling the table's
// function pointers directly, the way nvidia-smi does.

package main

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef unsigned int (*mockSlotFn)(void*, void*, void*, void*);

// mockExportTable returns the internal export table for the GUID nvidia-smi
// asks for, or NULL. The library is already loaded through go-nvml, so this
// dlopen only fetches a handle to it.
static void* mockExportTable(void) {
    void* lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_GLOBAL);
    if (lib == NULL) {
        return NULL;
    }
    unsigned int (*getTable)(void**, void*) =
        (unsigned int (*)(void**, void*))dlsym(lib, "nvmlInternalGetExportTable");
    if (getTable == NULL) {
        return NULL;
    }
    // 6c3efec4-8fc9-4e6c-a327-ee696e12f7c4, in the byte order the library
    // compares against: first three groups little-endian, the rest in order.
    unsigned char guid[16] = {
        0xc4, 0xfe, 0x3e, 0x6c, 0xc9, 0x8f, 0x6c, 0x4e,
        0xa3, 0x27, 0xee, 0x69, 0x6e, 0x12, 0xf7, 0xc4,
    };
    void* table = NULL;
    if (getTable(&table, guid) != 0) {
        return NULL;
    }
    return table;
}

static unsigned int mockCallSlot(void* table, unsigned int slot, void* a0, void* a1, void* a2, void* a3) {
    mockSlotFn fn = (mockSlotFn)((void**)table)[slot];
    if (fn == NULL) {
        return 0xFFFFFFFFu;
    }
    return fn(a0, a1, a2, a3);
}

// mockDeviceHandle asks the table for device index's handle, the value every
// other per-device slot expects as its first argument. The out array is two
// pointers wide so a handle struct larger than one pointer still fits.
static void* mockDeviceHandle(void* table, unsigned int slot, unsigned int index) {
    void* out[2] = {NULL, NULL};
    if (mockCallSlot(table, slot, (void*)(uintptr_t)index, out, (void*)0x22, NULL) != 0) {
        return NULL;
    }
    return out[0];
}
*/
import "C"

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"
)

// Export-table slots, as table index (byte offset / 8). Duplicated from the
// library on purpose: these tests pin the slots nvidia-smi calls, so a change on
// either side has to be deliberate.
const (
	slotDeviceHandleByIndex = 81
	slotProcessListFirst    = 213
	slotProcessListLast     = 215
	exportTableSlots        = 256
)

// Process-entry layout written by the internal process-list call, documented on
// mockInternalFillProcessList.
const (
	procEntryPidOffset    = 0
	procEntryMemoryOffset = 8
	procEntryNameOffset   = 32
	procEntrySize         = 4128
)

// Fixture values from util-test-config.yaml, device 0.
const (
	wantProcessPID       = 4242
	wantProcessName      = "python"
	wantProcessMemoryMiB = 6000
)

// guardFill is written across a buffer no call is allowed to touch, so a stray
// write shows up as a mismatch rather than as a crash whose symptom depends on
// what the address happened to be.
const guardFill = 0xAA

// testInternalExportTable drives the export table the way nvidia-smi does.
func testInternalExportTable() []testResult {
	var results []testResult

	table := C.mockExportTable()
	if table == nil {
		return append(results, testResult{"internal/export_table", false,
			"nvmlInternalGetExportTable returned no table for the nvidia-smi GUID"})
	}
	results = append(results, testResult{"internal/export_table", true, ""})

	handle := C.mockDeviceHandle(table, C.uint(slotDeviceHandleByIndex), 0)
	if handle == nil {
		return append(results, testResult{"internal/device_handle", false,
			fmt.Sprintf("slot %d did not return a handle for device 0", slotDeviceHandleByIndex)})
	}
	results = append(results, testResult{"internal/device_handle", true, ""})

	results = append(results, testInternalProcessList(table, handle)...)
	results = append(results, testInternalOtherSlotsDoNotWrite(table, handle)...)
	return results
}

// testInternalProcessList checks that the process-list slots report the
// configured process. Only meaningful against the fixture config.
func testInternalProcessList(table, handle unsafe.Pointer) []testResult {
	var results []testResult
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		return results
	}

	for slot := slotProcessListFirst; slot <= slotProcessListLast; slot++ {
		name := fmt.Sprintf("internal/process_list_slot_%d", slot)

		const capacity = 4
		buf := C.calloc(capacity, procEntrySize)
		if buf == nil {
			return append(results, testResult{name, false, "calloc failed"})
		}
		count := C.uint(capacity)

		ret := C.mockCallSlot(table, C.uint(slot), handle, unsafe.Pointer(&count), buf, nil)
		entries := readProcessEntries(buf, int(count))
		C.free(buf)

		switch {
		case ret != 0:
			results = append(results, testResult{name, false, fmt.Sprintf("returned %d, want NVML_SUCCESS", ret)})
		case len(entries) != 1:
			results = append(results, testResult{name, false,
				fmt.Sprintf("reported %d processes, want the 1 configured on device 0", len(entries))})
		case entries[0].pid != wantProcessPID || entries[0].name != wantProcessName ||
			entries[0].memoryMiB() != wantProcessMemoryMiB:
			results = append(results, testResult{name, false,
				fmt.Sprintf("entry = pid %d %q %d MiB; want pid %d %q %d MiB",
					entries[0].pid, entries[0].name, entries[0].memoryMiB(),
					wantProcessPID, wantProcessName, wantProcessMemoryMiB)})
		default:
			results = append(results, testResult{name, true, ""})
		}
	}
	return results
}

// testInternalOtherSlotsDoNotWrite is the regression guard for the fault that
// `nvidia-smi -q` hit whenever a process was configured. Slots outside the
// process-list range take fewer arguments, so their third register holds a
// leftover value rather than a caller buffer: deciding what to write from the
// argument shape alone turned those calls into writes through a pointer the
// caller never passed. Every such slot must leave the buffer alone and report
// zero entries.
func testInternalOtherSlotsDoNotWrite(table, handle unsafe.Pointer) []testResult {
	const name = "internal/other_slots_no_write"
	var results []testResult

	const guardSize = 2 * procEntrySize
	guard := C.malloc(guardSize)
	if guard == nil {
		return append(results, testResult{name, false, "malloc failed"})
	}
	defer C.free(guard)
	want := bytes.Repeat([]byte{guardFill}, guardSize)

	for slot := 1; slot < exportTableSlots; slot++ {
		if slot >= slotProcessListFirst && slot <= slotProcessListLast {
			continue
		}
		C.memset(guard, guardFill, guardSize)
		// 65535 is what nvidia-smi's uninitialized output variable held on the
		// call that faulted, and it sits inside any plausible capacity range.
		count := C.uint(65535)

		ret := C.mockCallSlot(table, C.uint(slot), handle, unsafe.Pointer(&count), guard, nil)
		got := C.GoBytes(guard, C.int(guardSize))

		switch {
		case !bytes.Equal(got, want):
			return append(results, testResult{name, false,
				fmt.Sprintf("slot %d wrote into a buffer it was never given (first bytes: %x)",
					slot, got[:16])})
		case ret != 0:
			return append(results, testResult{name, false,
				fmt.Sprintf("slot %d returned %d, want NVML_SUCCESS", slot, ret)})
		case count != 0:
			return append(results, testResult{name, false,
				fmt.Sprintf("slot %d left the count at %d, want 0: nvidia-smi then walks its "+
					"own uninitialized array and renders phantom processes", slot, count)})
		}
	}
	return append(results, testResult{name, true, ""})
}

// processEntry is one decoded entry of the internal process-list array.
type processEntry struct {
	pid       uint32
	usedBytes uint64
	name      string
}

func (e processEntry) memoryMiB() uint64 { return e.usedBytes / (1024 * 1024) }

func readProcessEntries(buf unsafe.Pointer, count int) []processEntry {
	entries := make([]processEntry, 0, count)
	for i := range count {
		e := unsafe.Add(buf, i*procEntrySize)
		entries = append(entries, processEntry{
			pid:       *(*uint32)(unsafe.Add(e, procEntryPidOffset)),
			usedBytes: *(*uint64)(unsafe.Add(e, procEntryMemoryOffset)),
			name:      C.GoString((*C.char)(unsafe.Add(e, procEntryNameOffset))),
		})
	}
	return entries
}
