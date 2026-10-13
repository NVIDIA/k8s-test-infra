// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#include <stddef.h>
#include <stdlib.h>
#include "nvml_types.h"
*/
import "C"

import "unsafe"

// Test files cannot use cgo, so these hooks report the C layouts and drive the
// exported entry points with caller-allocated structs, the way nvidia-smi does.

func uuidSizeForTest() uintptr           { return unsafe.Sizeof(C.nvmlUUID_t{}) }
func pdiSizeForTest() uintptr            { return unsafe.Sizeof(C.nvmlPdi_t{}) }
func addressingModeSizeForTest() uintptr { return unsafe.Sizeof(C.nvmlDeviceAddressingMode_t{}) }
func capabilitiesSizeForTest() uintptr   { return unsafe.Sizeof(C.nvmlDeviceCapabilities_t{}) }
func clkMonStatusSizeForTest() uintptr   { return unsafe.Sizeof(C.nvmlClkMonStatus_t{}) }
func busTypeSizeForTest() uintptr        { return unsafe.Sizeof(C.nvmlBusType_t(0)) }

func uuidValueOffsetForTest() uintptr {
	var u C.nvmlUUID_t
	return unsafe.Offsetof(u.value)
}

func pdiValueOffsetForTest() uintptr {
	var p C.nvmlPdi_t
	return unsafe.Offsetof(p.value)
}

// handleByUUIDVForTest looks a device up through nvmlDeviceGetHandleByUUIDV
// with the caller's version, type and value bytes.
func handleByUUIDVForTest(version, uuidType uint32, value []byte) (unsafe.Pointer, uint32) {
	var uuid C.nvmlUUID_t
	uuid.version = C.uint(version)
	uuid._type = C.uint(uuidType)
	copy(uuid.value[:], value) // cgo spells the C union as a byte array
	var device C.nvmlDevice_t
	ret := nvmlDeviceGetHandleByUUIDV(&uuid, &device)
	return unsafe.Pointer(device.handle), uint32(ret)
}

func handleBySerialForTest(serial string) (unsafe.Pointer, uint32) {
	cSerial := C.CString(serial)
	defer C.free(unsafe.Pointer(cSerial))
	var device C.nvmlDevice_t
	ret := nvmlDeviceGetHandleBySerial(cSerial, &device)
	return unsafe.Pointer(device.handle), uint32(ret)
}

func deviceForTest(handle unsafe.Pointer) C.nvmlDevice_t {
	return C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
}

func addressingModeForTest(handle unsafe.Pointer, version uint32) (uint32, uint32) {
	mode := C.nvmlDeviceAddressingMode_t{version: C.uint(version)}
	ret := nvmlDeviceGetAddressingMode(deviceForTest(handle), &mode)
	return uint32(mode.value), uint32(ret)
}

func pdiForTest(handle unsafe.Pointer, version uint32) (uint64, uint32) {
	pdi := C.nvmlPdi_t{version: C.uint(version)}
	ret := nvmlDeviceGetPdi(deviceForTest(handle), &pdi)
	return uint64(pdi.value), uint32(ret)
}

// clkMonStatusForTest hands the bridge a struct pre-filled with garbage, as an
// uninitialised caller buffer would be, and reports the fault list length and
// whether any byte of it survived.
func clkMonStatusForTest(handle unsafe.Pointer) (listSize uint32, dirty bool, ret uint32) {
	var status C.nvmlClkMonStatus_t
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&status)), unsafe.Sizeof(status))
	for i := range raw {
		raw[i] = 0xa5
	}
	ret = uint32(nvmlDeviceGetClkMonStatus(deviceForTest(handle), &status))
	for _, b := range raw {
		dirty = dirty || b != 0
	}
	return uint32(status.clkMonListSize), dirty, ret
}
