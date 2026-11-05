// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML device identity and PCIe attribute functions
// `nvidia-smi -q` renders in its header and PCI blocks:
//   - nvmlDeviceGetHandleBySerial, nvmlDeviceGetHandleByUUIDV
//   - nvmlDeviceGetNumGpuCores, nvmlDeviceGetAddressingMode,
//     nvmlDeviceGetCapabilities, nvmlDeviceGetPdi, nvmlDeviceGetIrqNum
//   - nvmlDeviceOnSameBoard, nvmlDeviceGetBridgeChipInfo
//   - nvmlDeviceGetBusType, nvmlDeviceGetPcieSpeed, nvmlDeviceGetPcieLinkMaxSpeed
//
// See issue NVIDIA/k8s-test-infra#1042.
package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "nvml_types.h"
*/
import "C"

import (
	"bytes"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// structV1Tag is the NVML_STRUCT_VERSION(…, 1) a caller must stamp into a
// versioned struct of the given size before handing it over.
func structV1Tag(size uintptr) C.uint {
	return C.uint(FabricStructVersion(size, 1))
}

//export nvmlDeviceGetHandleBySerial
func nvmlDeviceGetHandleBySerial(serial *C.char, device *C.nvmlDevice_t) C.nvmlReturn_t {
	if device == nil || serial == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle, ret := engine.GetEngine().DeviceGetHandleBySerial(C.GoString(serial))
	if ret == nvml.SUCCESS {
		device.handle = (*C.struct_nvmlDevice_st)(handle)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByUUIDV
func nvmlDeviceGetHandleByUUIDV(uuid *C.nvmlUUID_t, device *C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetHandleByUUIDV"); !ok {
		return ret
	}
	if device == nil || uuid == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uuid.version != structV1Tag(unsafe.Sizeof(*uuid)) {
		debugLog("[NVML] nvmlDeviceGetHandleByUUIDV rejected version 0x%x\n", uint32(uuid.version))
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	value := C.GoBytes(unsafe.Pointer(&uuid.value[0]), C.int(len(uuid.value)))

	var handle unsafe.Pointer
	var ret nvml.Return
	switch uuid._type {
	case C.NVML_UUID_TYPE_ASCII:
		// The string need not fill the buffer; nvmlDeviceGetUUID's own output
		// is 40 characters and a terminator.
		if end := bytes.IndexByte(value, 0); end >= 0 {
			value = value[:end]
		}
		handle, ret = engine.GetEngine().DeviceGetHandleByUUID(string(value))
	case C.NVML_UUID_TYPE_BINARY:
		handle, ret = engine.GetEngine().DeviceGetHandleByUUIDBinary([16]byte(value[:C.NVML_DEVICE_UUID_BINARY_LEN]))
	default:
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret == nvml.SUCCESS {
		device.handle = (*C.struct_nvmlDevice_st)(handle)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetNumGpuCores
func nvmlDeviceGetNumGpuCores(device C.nvmlDevice_t, numCores *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNumGpuCores"); !ok {
		return ret
	}
	if numCores == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	cores, ret := dev.GetNumGpuCores()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*numCores = C.uint(cores)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAddressingMode
func nvmlDeviceGetAddressingMode(device C.nvmlDevice_t, mode *C.nvmlDeviceAddressingMode_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAddressingMode"); !ok {
		return ret
	}
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if mode.version != structV1Tag(unsafe.Sizeof(*mode)) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetAddressingMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	mode.value = C.uint(got.Value)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCapabilities
func nvmlDeviceGetCapabilities(device C.nvmlDevice_t, caps *C.nvmlDeviceCapabilities_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCapabilities"); !ok {
		return ret
	}
	if caps == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if caps.version != structV1Tag(unsafe.Sizeof(*caps)) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetCapabilities()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	caps.capMask = C.uint(got.CapMask)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPdi
func nvmlDeviceGetPdi(device C.nvmlDevice_t, pdi *C.nvmlPdi_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPdi"); !ok {
		return ret
	}
	if pdi == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if pdi.version != structV1Tag(unsafe.Sizeof(*pdi)) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetPdi()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	pdi.value = C.ulonglong(got.Value)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetIrqNum
func nvmlDeviceGetIrqNum(device C.nvmlDevice_t, irqNum *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetIrqNum"); !ok {
		return ret
	}
	if irqNum == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	irq, ret := dev.GetIrqNum()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*irqNum = C.uint(irq)
	return C.NVML_SUCCESS
}

//export nvmlDeviceOnSameBoard
func nvmlDeviceOnSameBoard(device1 C.nvmlDevice_t, device2 C.nvmlDevice_t, onSameBoard *C.int) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceOnSameBoard"); !ok {
		return ret
	}
	if onSameBoard == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	e := engine.GetEngine()
	dev1 := e.LookupConfigurableDevice(unsafe.Pointer(device1.handle))
	dev2 := e.LookupConfigurableDevice(unsafe.Pointer(device2.handle))
	if dev1 == nil || dev2 == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	same, ret := dev1.OnSameBoard(dev2)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*onSameBoard = C.int(same)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetBridgeChipInfo
func nvmlDeviceGetBridgeChipInfo(device C.nvmlDevice_t, bridgeHierarchy *C.nvmlBridgeChipHierarchy_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetBridgeChipInfo"); !ok {
		return ret
	}
	if bridgeHierarchy == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The engine only ever declines, so the opaque struct is never written.
	_, ret := dev.GetBridgeChipInfo()
	return toReturn(ret)
}

//export nvmlDeviceGetBusType
func nvmlDeviceGetBusType(device C.nvmlDevice_t, _type *C.nvmlBusType_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetBusType"); !ok {
		return ret
	}
	if _type == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	busType, ret := dev.GetBusType()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*_type = C.nvmlBusType_t(busType)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPcieSpeed
func nvmlDeviceGetPcieSpeed(device C.nvmlDevice_t, pcieSpeed *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPcieSpeed"); !ok {
		return ret
	}
	if pcieSpeed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	speed, ret := dev.GetPcieSpeed()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*pcieSpeed = C.uint(speed)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPcieLinkMaxSpeed
func nvmlDeviceGetPcieLinkMaxSpeed(device C.nvmlDevice_t, maxSpeed *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPcieLinkMaxSpeed"); !ok {
		return ret
	}
	if maxSpeed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	speed, ret := dev.GetPcieLinkMaxSpeed()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*maxSpeed = C.uint(speed)
	return C.NVML_SUCCESS
}
