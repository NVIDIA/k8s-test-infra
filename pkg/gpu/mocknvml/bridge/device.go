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

// Package main provides NVML device handle functions.
// This file contains the hand-written implementations for:
// - nvmlDeviceGetCount, nvmlDeviceGetCount_v1, nvmlDeviceGetCount_v2
// - nvmlDeviceGetHandleByIndex, nvmlDeviceGetHandleByIndex_v1, nvmlDeviceGetHandleByIndex_v2
// - nvmlDeviceGetHandleByUUID
// - nvmlDeviceGetHandleByPciBusId, nvmlDeviceGetHandleByPciBusId_v1, nvmlDeviceGetHandleByPciBusId_v2
// - nvmlDeviceGetName
// - nvmlDeviceGetUUID
// - nvmlDeviceGetIndex
// - nvmlDeviceGetBrand
// - nvmlDeviceGetSerial
// - nvmlDeviceGetMinorNumber
// - nvmlDeviceGetPciInfo (v1, v2, v3)
// - nvmlDeviceGetMemoryInfo
// - nvmlDeviceGetMemoryInfo_v2
// - nvmlDeviceGetArchitecture
// - nvmlDeviceGetCudaComputeCapability
// - nvmlDeviceGetMigMode
// - nvmlDeviceGetComputeRunningProcesses_v3
// - nvmlDeviceGetProcessUtilization
// - nvmlDeviceGetPerformanceState
// - nvmlDeviceGetCurrentClocksEventReasons
// - nvmlDeviceGetPersistenceMode
// - nvmlDeviceSetPersistenceMode
// - nvmlDeviceGetRemappedRows
// - nvmlDeviceGetGspFirmwareMode
// - nvmlDeviceGetDisplayActive
// - nvmlDeviceGetMaxMigDeviceCount
// - nvmlDeviceGetMigDeviceHandleByIndex
// - nvmlGpmQueryDeviceSupport
// - nvmlDeviceGetPowerUsage
// - nvmlDeviceGetPowerManagementLimit
// - nvmlDeviceGetPowerManagementDefaultLimit
// - nvmlDeviceGetPowerManagementLimitConstraints
// - nvmlDeviceGetPowerState
// - nvmlDeviceGetTemperature
// - nvmlDeviceGetClockInfo
// - nvmlDeviceGetMaxClockInfo
// - nvmlDeviceGetApplicationsClock
// - nvmlDeviceGetDefaultApplicationsClock
// - nvmlDeviceGetCurrentClocksThrottleReasons
// - nvmlDeviceGetUtilizationRates
// - nvmlDeviceGetComputeMode
// - nvmlDeviceGetEccMode
// - nvmlDeviceGetDisplayMode
// - nvmlDeviceGetAccountingMode
// - nvmlDeviceGetGpuOperationMode
// - nvmlDeviceGetMultiGpuBoard
// - nvmlDeviceGetFanSpeed
// - nvmlDeviceGetFanSpeed_v2
// - nvmlDeviceGetNumFans
// - nvmlDeviceGetBAR1MemoryInfo
// - nvmlDeviceGetVbiosVersion
// - nvmlDeviceGetBoardPartNumber
// - nvmlDeviceGetInforomImageVersion
// - nvmlDeviceGetInforomVersion
// - nvmlDeviceGetCurrPcieLinkGeneration
// - nvmlDeviceGetCurrPcieLinkWidth
// - nvmlDeviceGetMaxPcieLinkGeneration
// - nvmlDeviceGetMaxPcieLinkWidth
// - nvmlDeviceGetPcieReplayCounter
// - nvmlDeviceGetPcieThroughput
// - nvmlDeviceGetTotalEccErrors
// - nvmlDeviceGetRetiredPages
// - nvmlDeviceGetRetiredPagesPendingStatus
// - nvmlDeviceGetBoardId
// - nvmlDeviceGetEncoderUtilization
// - nvmlDeviceGetDecoderUtilization
// - nvmlDeviceGetGraphicsRunningProcesses_v3
// - nvmlDeviceGetNvLinkVersion
// - nvmlDeviceGetNvLinkCapability
// - nvmlDeviceGetMemoryErrorCounter
// - nvmlDeviceGetMemoryBusWidth
// - nvmlDeviceGetDefaultEccMode
// - nvmlDeviceGetSupportedClocksThrottleReasons
// - nvmlDeviceGetAutoBoostedClocksEnabled
// - nvmlDeviceGetGspFirmwareVersion
// - nvmlDeviceGetTotalEnergyConsumption
// - nvmlDeviceGetDetailedEccErrors

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// =============================================================================
// Device Count Functions
// =============================================================================

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2(deviceCount unsafe.Pointer) C.nvmlReturn_t {
	if deviceCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	count, ret := engine.GetEngine().DeviceGetCount()
	if ret == nvml.SUCCESS {
		*(*C.uint)(deviceCount) = C.uint(count)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetCount
func nvmlDeviceGetCount(deviceCount unsafe.Pointer) C.nvmlReturn_t {
	return nvmlDeviceGetCount_v2(deviceCount)
}

//export nvmlDeviceGetCount_v1
func nvmlDeviceGetCount_v1(deviceCount unsafe.Pointer) C.nvmlReturn_t {
	return nvmlDeviceGetCount_v2(deviceCount)
}

// =============================================================================
// Device Handle Functions
// =============================================================================

//export nvmlDeviceGetHandleByIndex_v2
func nvmlDeviceGetHandleByIndex_v2(index C.uint, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if nvmlDevice == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	debugLog("[NVML] nvmlDeviceGetHandleByIndex(%d)\n", index)
	handle, ret := engine.GetEngine().DeviceGetHandleByIndex(int(index))
	if ret == nvml.SUCCESS {
		// nvmlDevice_t is a struct with a handle field pointing to opaque nvmlDevice_st
		//nolint:govet // Converting uintptr to unsafe.Pointer is intentional - handle was allocated
		// as C memory by HandleTable.Register() and we need to pass it back to the C caller
		nvmlDevice.handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(handle))
		debugLog("[NVML]   -> handle=0x%x ret=%d\n", uintptr(handle), ret)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByIndex
func nvmlDeviceGetHandleByIndex(index C.uint, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByIndex_v2(index, nvmlDevice)
}

//export nvmlDeviceGetHandleByIndex_v1
func nvmlDeviceGetHandleByIndex_v1(index C.uint, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByIndex_v2(index, nvmlDevice)
}

//export nvmlDeviceGetHandleByUUID
func nvmlDeviceGetHandleByUUID(uuid *C.char, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if nvmlDevice == nil || uuid == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	goUUID := C.GoString(uuid)
	handle, ret := engine.GetEngine().DeviceGetHandleByUUID(goUUID)
	if ret == nvml.SUCCESS {
		//nolint:govet // Converting uintptr to unsafe.Pointer is intentional - handle was allocated
		// as C memory by HandleTable.Register() and we need to pass it back to the C caller
		nvmlDevice.handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(handle))
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByPciBusId_v2
func nvmlDeviceGetHandleByPciBusId_v2(pciBusId *C.char, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if nvmlDevice == nil || pciBusId == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	goPciBusId := C.GoString(pciBusId)
	handle, ret := engine.GetEngine().DeviceGetHandleByPciBusId(goPciBusId)
	if ret == nvml.SUCCESS {
		//nolint:govet // Converting uintptr to unsafe.Pointer is intentional - handle was allocated
		// as C memory by HandleTable.Register() and we need to pass it back to the C caller
		nvmlDevice.handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(handle))
	}
	return toReturn(ret)
}

//export nvmlDeviceGetHandleByPciBusId_v1
func nvmlDeviceGetHandleByPciBusId_v1(pciBusId *C.char, nvmlDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	return nvmlDeviceGetHandleByPciBusId_v2(pciBusId, nvmlDevice)
}

// =============================================================================
// Device Info Functions (Basic)
// =============================================================================

//export nvmlDeviceGetName
func nvmlDeviceGetName(nvmlDevice C.nvmlDevice_t, name *C.char, length C.uint) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	devName, ret := dev.GetName()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(devName, name, length)
}

//export nvmlDeviceGetUUID
func nvmlDeviceGetUUID(nvmlDevice C.nvmlDevice_t, uuid *C.char, length C.uint) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	devUUID, ret := dev.GetUUID()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(devUUID, uuid, length)
}

//export nvmlDeviceGetIndex
func nvmlDeviceGetIndex(nvmlDevice C.nvmlDevice_t, index *C.uint) C.nvmlReturn_t {
	if index == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	idx, ret := dev.GetIndex()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*index = C.uint(idx)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetBrand
func nvmlDeviceGetBrand(nvmlDevice C.nvmlDevice_t, _type *C.nvmlBrandType_t) C.nvmlReturn_t {
	if _type == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	brand, ret := dev.GetBrand()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*_type = C.nvmlBrandType_t(brand)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSerial
func nvmlDeviceGetSerial(nvmlDevice C.nvmlDevice_t, serial *C.char, length C.uint) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	devSerial, ret := dev.GetSerial()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(devSerial, serial, length)
}

//export nvmlDeviceGetMinorNumber
func nvmlDeviceGetMinorNumber(nvmlDevice C.nvmlDevice_t, minorNumber *C.uint) C.nvmlReturn_t {
	if minorNumber == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	minor, ret := dev.GetMinorNumber()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*minorNumber = C.uint(minor)
	return C.NVML_SUCCESS
}

// =============================================================================
// Device PCI Info Functions
// =============================================================================

//export nvmlDeviceGetPciInfo_v3
func nvmlDeviceGetPciInfo_v3(nvmlDevice C.nvmlDevice_t, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	if pci == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	info, ret := dev.GetPciInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// Copy BusIdLegacy (16 bytes = NVML_DEVICE_PCI_BUS_ID_LEGACY_FMT_SIZE)
	for i := 0; i < len(info.BusIdLegacy) && i < 16; i++ {
		pci.busIdLegacy[i] = C.char(info.BusIdLegacy[i])
	}
	pci.domain = C.uint(info.Domain)
	pci.bus = C.uint(info.Bus)
	pci.device = C.uint(info.Device)
	pci.pciDeviceId = C.uint(info.PciDeviceId)
	pci.pciSubSystemId = C.uint(info.PciSubSystemId)
	// Copy BusId (32 bytes = NVML_DEVICE_PCI_BUS_ID_BUFFER_SIZE)
	for i := 0; i < len(info.BusId) && i < 32; i++ {
		pci.busId[i] = C.char(info.BusId[i])
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPciInfo_v2
func nvmlDeviceGetPciInfo_v2(nvmlDevice C.nvmlDevice_t, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetPciInfo_v3(nvmlDevice, pci)
}

//export nvmlDeviceGetPciInfo_v1
func nvmlDeviceGetPciInfo_v1(nvmlDevice C.nvmlDevice_t, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	return nvmlDeviceGetPciInfo_v3(nvmlDevice, pci)
}

// =============================================================================
// Device Memory Info Functions
// =============================================================================

//export nvmlDeviceGetMemoryInfo
func nvmlDeviceGetMemoryInfo(nvmlDevice C.nvmlDevice_t, memory *C.nvmlMemory_t) C.nvmlReturn_t {
	if memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	mem, ret := dev.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	memory.total = C.ulonglong(mem.Total)
	memory.free = C.ulonglong(mem.Free)
	memory.used = C.ulonglong(mem.Used)
	return C.NVML_SUCCESS
}

// =============================================================================
// Topology Functions
// =============================================================================

//export nvmlDeviceGetTopologyCommonAncestor
func nvmlDeviceGetTopologyCommonAncestor(device1 C.nvmlDevice_t, device2 C.nvmlDevice_t, pathInfo *C.nvmlGpuTopologyLevel_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTopologyCommonAncestor"); !ok {
		return ret
	}
	if pathInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle1 := uintptr(unsafe.Pointer(device1.handle))
	handle2 := uintptr(unsafe.Pointer(device2.handle))
	dev1 := engine.GetEngine().LookupConfigurableDevice(handle1)
	if dev1 == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev2 := engine.GetEngine().LookupDevice(handle2)
	if dev2 == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	level, ret := dev1.GetTopologyCommonAncestor(dev2)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*pathInfo = C.nvmlGpuTopologyLevel_t(level)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetTopologyNearestGpus
func nvmlDeviceGetTopologyNearestGpus(device C.nvmlDevice_t, level C.nvmlGpuTopologyLevel_t, count *C.uint, deviceArray *C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTopologyNearestGpus"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Return empty array - topology nearest GPUs is complex and rarely used in mocks
	*count = 0
	return C.NVML_SUCCESS
}

// =============================================================================
// NVLink Functions
// =============================================================================

//export nvmlDeviceGetNvLinkState
func nvmlDeviceGetNvLinkState(device C.nvmlDevice_t, link C.uint, isActive *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkState"); !ok {
		return ret
	}
	if isActive == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	state, ret := dev.GetNvLinkState(int(link))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*isActive = C.nvmlEnableState_t(state)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvLinkErrorCounter
func nvmlDeviceGetNvLinkErrorCounter(device C.nvmlDevice_t, link C.uint, counter C.nvmlNvLinkErrorCounter_t, counterValue *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkErrorCounter"); !ok {
		return ret
	}
	if counterValue == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetNvLinkErrorCounter(int(link), nvml.NvLinkErrorCounter(counter))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*counterValue = C.ulonglong(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvLinkRemotePciInfo_v2
func nvmlDeviceGetNvLinkRemotePciInfo_v2(device C.nvmlDevice_t, link C.uint, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkRemotePciInfo_v2"); !ok {
		return ret
	}
	if pci == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetNvLinkRemotePciInfo(int(link))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	pci.domain = C.uint(info.Domain)
	pci.bus = C.uint(info.Bus)
	pci.device = C.uint(info.Device)
	for i := 0; i < len(info.BusId) && i < 32; i++ {
		pci.busId[i] = C.char(info.BusId[i])
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvLinkRemotePciInfo_v1
func nvmlDeviceGetNvLinkRemotePciInfo_v1(device C.nvmlDevice_t, link C.uint, pci *C.nvmlPciInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkRemotePciInfo_v1"); !ok {
		return ret
	}
	return nvmlDeviceGetNvLinkRemotePciInfo_v2(device, link, pci)
}

// =============================================================================
// Temperature / Thermal Functions
// =============================================================================

//export nvmlDeviceGetTemperatureThreshold
func nvmlDeviceGetTemperatureThreshold(device C.nvmlDevice_t, thresholdType C.nvmlTemperatureThresholds_t, temp *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTemperatureThreshold"); !ok {
		return ret
	}
	if temp == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	t, ret := dev.GetTemperatureThreshold(nvml.TemperatureThresholds(thresholdType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*temp = C.uint(t)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetThermalSettings
func nvmlDeviceGetThermalSettings(device C.nvmlDevice_t, sensorIndex C.uint, pThermalSettings *C.nvmlGpuThermalSettings_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetThermalSettings"); !ok {
		return ret
	}
	if pThermalSettings == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	settings, ret := dev.GetThermalSettings(uint32(sensorIndex))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	pThermalSettings.count = C.uint(settings.Count)
	if settings.Count > 0 {
		pThermalSettings.sensor[0].controller = C.NVML_THERMAL_CONTROLLER_GPU_INTERNAL
		if dev != nil && dev.GetConfig() != nil && dev.GetConfig().Thermal != nil {
			thermal := dev.GetConfig().Thermal
			pThermalSettings.sensor[0].currentTemp = C.int(thermal.TemperatureGPU_C)
			pThermalSettings.sensor[0].defaultMaxTemp = C.int(thermal.MaxOperating_C)
			pThermalSettings.sensor[0].target = C.NVML_THERMAL_TARGET_GPU
		}
	}
	return C.NVML_SUCCESS
}

// =============================================================================
// Power Functions
// =============================================================================

//export nvmlDeviceGetEnforcedPowerLimit
func nvmlDeviceGetEnforcedPowerLimit(device C.nvmlDevice_t, limit *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetEnforcedPowerLimit"); !ok {
		return ret
	}
	if limit == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	l, ret := dev.GetEnforcedPowerLimit()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*limit = C.uint(l)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerManagementMode
func nvmlDeviceGetPowerManagementMode(device C.nvmlDevice_t, mode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerManagementMode"); !ok {
		return ret
	}
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	m, ret := dev.GetPowerManagementMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*mode = C.nvmlEnableState_t(m)
	return C.NVML_SUCCESS
}

// =============================================================================
// MIG Functions
// =============================================================================

//export nvmlDeviceGetMaxMigDeviceCount
func nvmlDeviceGetMaxMigDeviceCount(device C.nvmlDevice_t, count *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMaxMigDeviceCount"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	c, ret := dev.GetMaxMigDeviceCount()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*count = C.uint(c)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMigDeviceHandleByIndex
func nvmlDeviceGetMigDeviceHandleByIndex(device C.nvmlDevice_t, index C.uint, migDevice *C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMigDeviceHandleByIndex"); !ok {
		return ret
	}
	if migDevice == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	_, ret := dev.GetMigDeviceHandleByIndex(int(index))
	return toReturn(ret)
}

// =============================================================================
// GPM Functions
// =============================================================================

//export nvmlGpmQueryDeviceSupport
func nvmlGpmQueryDeviceSupport(device C.nvmlDevice_t, gpmSupport *C.nvmlGpmSupport_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpmQueryDeviceSupport"); !ok {
		return ret
	}
	if gpmSupport == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	supported, ret := dev.GetGpmSupport()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	gpmSupport.isSupportedDevice = C.uint(supported)
	return C.NVML_SUCCESS
}

// =============================================================================
// Device Memory Info v2, Architecture, CUDA Compute, MIG Mode
// =============================================================================

//export nvmlDeviceGetMemoryInfo_v2
func nvmlDeviceGetMemoryInfo_v2(nvmlDevice C.nvmlDevice_t, memory *C.nvmlMemory_v2_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMemoryInfo_v2"); !ok {
		return ret
	}
	if memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	mem, ret := dev.GetMemoryInfo_v2()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	memory.version = C.uint(mem.Version)
	memory.total = C.ulonglong(mem.Total)
	memory.reserved = C.ulonglong(mem.Reserved)
	memory.free = C.ulonglong(mem.Free)
	memory.used = C.ulonglong(mem.Used)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetArchitecture
func nvmlDeviceGetArchitecture(nvmlDevice C.nvmlDevice_t, arch *C.nvmlDeviceArchitecture_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetArchitecture"); !ok {
		return ret
	}
	if arch == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	architecture, ret := dev.GetArchitecture()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*arch = C.nvmlDeviceArchitecture_t(architecture)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCudaComputeCapability
func nvmlDeviceGetCudaComputeCapability(nvmlDevice C.nvmlDevice_t, major *C.int, minor *C.int) C.nvmlReturn_t {
	if major == nil || minor == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	maj, min, ret := dev.GetCudaComputeCapability()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*major = C.int(maj)
	*minor = C.int(min)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMigMode
func nvmlDeviceGetMigMode(nvmlDevice C.nvmlDevice_t, currentMode *C.uint, pendingMode *C.uint) C.nvmlReturn_t {
	if currentMode == nil || pendingMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupDevice(handle)
	current, pending, ret := dev.GetMigMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*currentMode = C.uint(current)
	*pendingMode = C.uint(pending)
	return C.NVML_SUCCESS
}

// =============================================================================
// Process Functions
// =============================================================================

//export nvmlDeviceGetComputeRunningProcesses_v3
func nvmlDeviceGetComputeRunningProcesses_v3(nvmlDevice C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetComputeRunningProcesses_v3"); !ok {
		return ret
	}
	if infoCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	procs, ret := dev.GetComputeRunningProcesses()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	if infos == nil {
		// Caller is querying the count
		*infoCount = C.uint(len(procs))
		return C.NVML_SUCCESS
	}
	bufSize := int(*infoCount)
	if len(procs) > bufSize {
		*infoCount = C.uint(len(procs))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	*infoCount = C.uint(len(procs))
	if len(procs) > 0 {
		// Write process info to the caller's buffer
		outSlice := unsafe.Slice(infos, len(procs))
		for i, p := range procs {
			outSlice[i].pid = C.uint(p.Pid)
			outSlice[i].usedGpuMemory = C.ulonglong(p.UsedGpuMemory)
			outSlice[i].gpuInstanceId = C.uint(p.GpuInstanceId)
			outSlice[i].computeInstanceId = C.uint(p.ComputeInstanceId)
		}
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetProcessUtilization
func nvmlDeviceGetProcessUtilization(nvmlDevice C.nvmlDevice_t, utilization *C.nvmlProcessUtilizationSample_t, processSamplesCount *C.uint, lastSeenTimeStamp C.ulonglong) C.nvmlReturn_t {
	if processSamplesCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	samples, ret := dev.GetProcessUtilization(uint64(lastSeenTimeStamp))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*processSamplesCount = C.uint(len(samples))
	return C.NVML_SUCCESS
}

// =============================================================================
// Performance Functions
// =============================================================================

//export nvmlDeviceGetPerformanceState
func nvmlDeviceGetPerformanceState(nvmlDevice C.nvmlDevice_t, pState *C.nvmlPstates_t) C.nvmlReturn_t {
	if pState == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	state, ret := dev.GetPerformanceState()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*pState = C.nvmlPstates_t(state)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCurrentClocksEventReasons
func nvmlDeviceGetCurrentClocksEventReasons(nvmlDevice C.nvmlDevice_t, clocksEventReasons *C.ulonglong) C.nvmlReturn_t {
	if clocksEventReasons == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	reasons, ret := dev.GetCurrentClocksEventReasons()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*clocksEventReasons = C.ulonglong(reasons)
	return C.NVML_SUCCESS
}

// =============================================================================
// Persistence Functions
// =============================================================================

//export nvmlDeviceGetPersistenceMode
func nvmlDeviceGetPersistenceMode(nvmlDevice C.nvmlDevice_t, mode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	state, ret := dev.GetPersistenceMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*mode = C.nvmlEnableState_t(state)
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetPersistenceMode
func nvmlDeviceSetPersistenceMode(nvmlDevice C.nvmlDevice_t, mode C.nvmlEnableState_t) C.nvmlReturn_t {
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	ret := dev.SetPersistenceMode(nvml.EnableState(mode))
	return toReturn(ret)
}

// =============================================================================
// Advanced Functions
// =============================================================================

//export nvmlDeviceGetRemappedRows
func nvmlDeviceGetRemappedRows(nvmlDevice C.nvmlDevice_t, corrRows *C.uint, uncRows *C.uint, isPending *C.uint, failureOccurred *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetRemappedRows"); !ok {
		return ret
	}
	if corrRows == nil || uncRows == nil || isPending == nil || failureOccurred == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	corr, unc, pending, failure, ret := dev.GetRemappedRows()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*corrRows = C.uint(corr)
	*uncRows = C.uint(unc)
	if pending {
		*isPending = 1
	} else {
		*isPending = 0
	}
	if failure {
		*failureOccurred = 1
	} else {
		*failureOccurred = 0
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGspFirmwareMode
func nvmlDeviceGetGspFirmwareMode(nvmlDevice C.nvmlDevice_t, isEnabled *C.uint, defaultMode *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGspFirmwareMode"); !ok {
		return ret
	}
	if isEnabled == nil || defaultMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	enabled, defMode, ret := dev.GetGspFirmwareMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	if enabled {
		*isEnabled = 1
	} else {
		*isEnabled = 0
	}
	if defMode {
		*defaultMode = 1
	} else {
		*defaultMode = 0
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetDisplayActive
func nvmlDeviceGetDisplayActive(nvmlDevice C.nvmlDevice_t, isActive *C.nvmlEnableState_t) C.nvmlReturn_t {
	if isActive == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(nvmlDevice.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	active, ret := dev.GetDisplayActive()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*isActive = C.nvmlEnableState_t(active)
	return C.NVML_SUCCESS
}

// =============================================================================
// Group A — Power Functions
// =============================================================================

//export nvmlDeviceGetPowerUsage
func nvmlDeviceGetPowerUsage(device C.nvmlDevice_t, power *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerUsage"); !ok {
		return ret
	}
	if power == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetPowerUsage()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*power = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerManagementLimit
func nvmlDeviceGetPowerManagementLimit(device C.nvmlDevice_t, limit *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerManagementLimit"); !ok {
		return ret
	}
	if limit == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetPowerManagementLimit()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*limit = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerManagementDefaultLimit
func nvmlDeviceGetPowerManagementDefaultLimit(device C.nvmlDevice_t, defaultLimit *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerManagementDefaultLimit"); !ok {
		return ret
	}
	if defaultLimit == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetPowerManagementDefaultLimit()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*defaultLimit = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerManagementLimitConstraints
func nvmlDeviceGetPowerManagementLimitConstraints(device C.nvmlDevice_t, minLimit *C.uint, maxLimit *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerManagementLimitConstraints"); !ok {
		return ret
	}
	if minLimit == nil || maxLimit == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mn, mx, ret := dev.GetPowerManagementLimitConstraints()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*minLimit = C.uint(mn)
	*maxLimit = C.uint(mx)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerState
func nvmlDeviceGetPowerState(device C.nvmlDevice_t, pState *C.nvmlPstates_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerState"); !ok {
		return ret
	}
	if pState == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	state, ret := dev.GetPowerState()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*pState = C.nvmlPstates_t(state)
	return C.NVML_SUCCESS
}

// =============================================================================
// Group B — Temperature + Clocks
// =============================================================================

//export nvmlDeviceGetTemperature
func nvmlDeviceGetTemperature(device C.nvmlDevice_t, sensorType C.nvmlTemperatureSensors_t, temp *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTemperature"); !ok {
		return ret
	}
	if temp == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetTemperature(nvml.TemperatureSensors(sensorType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*temp = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetClockInfo
func nvmlDeviceGetClockInfo(device C.nvmlDevice_t, clockType C.nvmlClockType_t, clock *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetClockInfo"); !ok {
		return ret
	}
	if clock == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetClockInfo(nvml.ClockType(clockType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*clock = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMaxClockInfo
func nvmlDeviceGetMaxClockInfo(device C.nvmlDevice_t, clockType C.nvmlClockType_t, clock *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMaxClockInfo"); !ok {
		return ret
	}
	if clock == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetMaxClockInfo(nvml.ClockType(clockType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*clock = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetApplicationsClock
func nvmlDeviceGetApplicationsClock(device C.nvmlDevice_t, clockType C.nvmlClockType_t, clockMHz *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetApplicationsClock"); !ok {
		return ret
	}
	if clockMHz == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetApplicationsClock(nvml.ClockType(clockType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*clockMHz = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetDefaultApplicationsClock
func nvmlDeviceGetDefaultApplicationsClock(device C.nvmlDevice_t, clockType C.nvmlClockType_t, clockMHz *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetDefaultApplicationsClock"); !ok {
		return ret
	}
	if clockMHz == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetDefaultApplicationsClock(nvml.ClockType(clockType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*clockMHz = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCurrentClocksThrottleReasons
func nvmlDeviceGetCurrentClocksThrottleReasons(device C.nvmlDevice_t, clocksThrottleReasons *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCurrentClocksThrottleReasons"); !ok {
		return ret
	}
	if clocksThrottleReasons == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetCurrentClocksThrottleReasons()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*clocksThrottleReasons = C.ulonglong(val)
	return C.NVML_SUCCESS
}

// =============================================================================
// Group C — Utilization + Mode + Display
// =============================================================================

//export nvmlDeviceGetUtilizationRates
func nvmlDeviceGetUtilizationRates(device C.nvmlDevice_t, utilization *C.nvmlUtilization_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetUtilizationRates"); !ok {
		return ret
	}
	if utilization == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	util, ret := dev.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	utilization.gpu = C.uint(util.Gpu)
	utilization.memory = C.uint(util.Memory)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetComputeMode
func nvmlDeviceGetComputeMode(device C.nvmlDevice_t, mode *C.nvmlComputeMode_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetComputeMode"); !ok {
		return ret
	}
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetComputeMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*mode = C.nvmlComputeMode_t(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetEccMode
func nvmlDeviceGetEccMode(device C.nvmlDevice_t, current *C.nvmlEnableState_t, pending *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetEccMode"); !ok {
		return ret
	}
	if current == nil || pending == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	cur, pend, ret := dev.GetEccMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*current = C.nvmlEnableState_t(cur)
	*pending = C.nvmlEnableState_t(pend)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetDisplayMode
func nvmlDeviceGetDisplayMode(device C.nvmlDevice_t, mode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetDisplayMode"); !ok {
		return ret
	}
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetDisplayMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*mode = C.nvmlEnableState_t(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAccountingMode
func nvmlDeviceGetAccountingMode(device C.nvmlDevice_t, mode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAccountingMode"); !ok {
		return ret
	}
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetAccountingMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*mode = C.nvmlEnableState_t(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGpuOperationMode
func nvmlDeviceGetGpuOperationMode(device C.nvmlDevice_t, current *C.nvmlGpuOperationMode_t, pending *C.nvmlGpuOperationMode_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuOperationMode"); !ok {
		return ret
	}
	if current == nil || pending == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	cur, pend, ret := dev.GetGpuOperationMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*current = C.nvmlGpuOperationMode_t(cur)
	*pending = C.nvmlGpuOperationMode_t(pend)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMultiGpuBoard
func nvmlDeviceGetMultiGpuBoard(device C.nvmlDevice_t, multiGpuBool *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMultiGpuBoard"); !ok {
		return ret
	}
	if multiGpuBool == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetMultiGpuBoard()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*multiGpuBool = C.uint(val)
	return C.NVML_SUCCESS
}

// =============================================================================
// Group D — Fan + BAR1 + InfoROM + VBIOS
// =============================================================================

//export nvmlDeviceGetFanSpeed
func nvmlDeviceGetFanSpeed(device C.nvmlDevice_t, speed *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetFanSpeed"); !ok {
		return ret
	}
	if speed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetFanSpeed()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*speed = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetFanSpeed_v2
func nvmlDeviceGetFanSpeed_v2(device C.nvmlDevice_t, fan C.uint, speed *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetFanSpeed_v2"); !ok {
		return ret
	}
	if speed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetFanSpeed_v2(int(fan))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*speed = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNumFans
func nvmlDeviceGetNumFans(device C.nvmlDevice_t, numFans *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNumFans"); !ok {
		return ret
	}
	if numFans == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetNumFans()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*numFans = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetBAR1MemoryInfo
func nvmlDeviceGetBAR1MemoryInfo(device C.nvmlDevice_t, bar1Memory *C.nvmlBAR1Memory_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetBAR1MemoryInfo"); !ok {
		return ret
	}
	if bar1Memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	b, ret := dev.GetBAR1MemoryInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	bar1Memory.bar1Total = C.ulonglong(b.Bar1Total)
	bar1Memory.bar1Free = C.ulonglong(b.Bar1Free)
	bar1Memory.bar1Used = C.ulonglong(b.Bar1Used)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetVbiosVersion
func nvmlDeviceGetVbiosVersion(device C.nvmlDevice_t, version *C.char, length C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetVbiosVersion"); !ok {
		return ret
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetVbiosVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(val, version, length)
}

//export nvmlDeviceGetBoardPartNumber
func nvmlDeviceGetBoardPartNumber(device C.nvmlDevice_t, partNumber *C.char, length C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetBoardPartNumber"); !ok {
		return ret
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetBoardPartNumber()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(val, partNumber, length)
}

//export nvmlDeviceGetInforomImageVersion
func nvmlDeviceGetInforomImageVersion(device C.nvmlDevice_t, version *C.char, length C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetInforomImageVersion"); !ok {
		return ret
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetInforomImageVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(val, version, length)
}

//export nvmlDeviceGetInforomVersion
func nvmlDeviceGetInforomVersion(device C.nvmlDevice_t, object C.nvmlInforomObject_t, version *C.char, length C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetInforomVersion"); !ok {
		return ret
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetInforomVersion(nvml.InforomObject(object))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(val, version, length)
}

// =============================================================================
// Group E — PCIe + ECC + Retired Pages
// =============================================================================

//export nvmlDeviceGetCurrPcieLinkGeneration
func nvmlDeviceGetCurrPcieLinkGeneration(device C.nvmlDevice_t, currLinkGen *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCurrPcieLinkGeneration"); !ok {
		return ret
	}
	if currLinkGen == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetCurrPcieLinkGeneration()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*currLinkGen = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCurrPcieLinkWidth
func nvmlDeviceGetCurrPcieLinkWidth(device C.nvmlDevice_t, currLinkWidth *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCurrPcieLinkWidth"); !ok {
		return ret
	}
	if currLinkWidth == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetCurrPcieLinkWidth()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*currLinkWidth = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMaxPcieLinkGeneration
func nvmlDeviceGetMaxPcieLinkGeneration(device C.nvmlDevice_t, maxLinkGen *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMaxPcieLinkGeneration"); !ok {
		return ret
	}
	if maxLinkGen == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetMaxPcieLinkGeneration()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*maxLinkGen = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMaxPcieLinkWidth
func nvmlDeviceGetMaxPcieLinkWidth(device C.nvmlDevice_t, maxLinkWidth *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMaxPcieLinkWidth"); !ok {
		return ret
	}
	if maxLinkWidth == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetMaxPcieLinkWidth()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*maxLinkWidth = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPcieReplayCounter
func nvmlDeviceGetPcieReplayCounter(device C.nvmlDevice_t, value *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPcieReplayCounter"); !ok {
		return ret
	}
	if value == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetPcieReplayCounter()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*value = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPcieThroughput
func nvmlDeviceGetPcieThroughput(device C.nvmlDevice_t, counter C.nvmlPcieUtilCounter_t, value *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPcieThroughput"); !ok {
		return ret
	}
	if value == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetPcieThroughput(nvml.PcieUtilCounter(counter))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*value = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetTotalEccErrors
func nvmlDeviceGetTotalEccErrors(device C.nvmlDevice_t, errorType C.nvmlMemoryErrorType_t, counterType C.nvmlEccCounterType_t, eccCounts *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTotalEccErrors"); !ok {
		return ret
	}
	if eccCounts == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetTotalEccErrors(nvml.MemoryErrorType(errorType), nvml.EccCounterType(counterType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*eccCounts = C.ulonglong(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetRetiredPages
func nvmlDeviceGetRetiredPages(device C.nvmlDevice_t, cause C.nvmlPageRetirementCause_t, pageCount *C.uint, addresses *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetRetiredPages"); !ok {
		return ret
	}
	if pageCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	pages, ret := dev.GetRetiredPages(nvml.PageRetirementCause(cause))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	if addresses == nil {
		// Caller is querying the count
		*pageCount = C.uint(len(pages))
		return C.NVML_SUCCESS
	}
	bufSize := int(*pageCount)
	if len(pages) > bufSize {
		*pageCount = C.uint(len(pages))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	*pageCount = C.uint(len(pages))
	if len(pages) > 0 {
		outSlice := unsafe.Slice(addresses, len(pages))
		for i, p := range pages {
			outSlice[i] = C.ulonglong(p)
		}
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetRetiredPagesPendingStatus
func nvmlDeviceGetRetiredPagesPendingStatus(device C.nvmlDevice_t, isPending *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetRetiredPagesPendingStatus"); !ok {
		return ret
	}
	if isPending == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetRetiredPagesPendingStatus()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*isPending = C.nvmlEnableState_t(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetBoardId
func nvmlDeviceGetBoardId(device C.nvmlDevice_t, boardId *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetBoardId"); !ok {
		return ret
	}
	if boardId == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetBoardId()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*boardId = C.uint(val)
	return C.NVML_SUCCESS
}

// =============================================================================
// Group F — Encoder/Decoder + Process + NVLink extras
// =============================================================================

//export nvmlDeviceGetEncoderUtilization
func nvmlDeviceGetEncoderUtilization(device C.nvmlDevice_t, utilization *C.uint, samplingPeriodUs *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetEncoderUtilization"); !ok {
		return ret
	}
	if utilization == nil || samplingPeriodUs == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	util, period, ret := dev.GetEncoderUtilization()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*utilization = C.uint(util)
	*samplingPeriodUs = C.uint(period)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetDecoderUtilization
func nvmlDeviceGetDecoderUtilization(device C.nvmlDevice_t, utilization *C.uint, samplingPeriodUs *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetDecoderUtilization"); !ok {
		return ret
	}
	if utilization == nil || samplingPeriodUs == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	util, period, ret := dev.GetDecoderUtilization()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*utilization = C.uint(util)
	*samplingPeriodUs = C.uint(period)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGraphicsRunningProcesses_v3
func nvmlDeviceGetGraphicsRunningProcesses_v3(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGraphicsRunningProcesses_v3"); !ok {
		return ret
	}
	if infoCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	procs, ret := dev.GetGraphicsRunningProcesses()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	if infos == nil {
		// Caller is querying the count
		*infoCount = C.uint(len(procs))
		return C.NVML_SUCCESS
	}
	bufSize := int(*infoCount)
	if len(procs) > bufSize {
		*infoCount = C.uint(len(procs))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	*infoCount = C.uint(len(procs))
	if len(procs) > 0 {
		outSlice := unsafe.Slice(infos, len(procs))
		for i, p := range procs {
			outSlice[i].pid = C.uint(p.Pid)
			outSlice[i].usedGpuMemory = C.ulonglong(p.UsedGpuMemory)
			outSlice[i].gpuInstanceId = C.uint(p.GpuInstanceId)
			outSlice[i].computeInstanceId = C.uint(p.ComputeInstanceId)
		}
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvLinkVersion
func nvmlDeviceGetNvLinkVersion(device C.nvmlDevice_t, link C.uint, version *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkVersion"); !ok {
		return ret
	}
	if version == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetNvLinkVersion(int(link))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*version = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvLinkCapability
func nvmlDeviceGetNvLinkCapability(device C.nvmlDevice_t, link C.uint, capability C.nvmlNvLinkCapability_t, capResult *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkCapability"); !ok {
		return ret
	}
	if capResult == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetNvLinkCapability(int(link), nvml.NvLinkCapability(capability))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*capResult = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMemoryErrorCounter
func nvmlDeviceGetMemoryErrorCounter(device C.nvmlDevice_t, errorType C.nvmlMemoryErrorType_t, counterType C.nvmlEccCounterType_t, locationType C.nvmlMemoryLocation_t, count *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMemoryErrorCounter"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetMemoryErrorCounter(nvml.MemoryErrorType(errorType), nvml.EccCounterType(counterType), nvml.MemoryLocation(locationType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*count = C.ulonglong(val)
	return C.NVML_SUCCESS
}

// =============================================================================
// Group G — nvidia-smi query gap closures
// =============================================================================

//export nvmlDeviceGetMemoryBusWidth
func nvmlDeviceGetMemoryBusWidth(device C.nvmlDevice_t, busWidth *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMemoryBusWidth"); !ok {
		return ret
	}
	if busWidth == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetMemoryBusWidth()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*busWidth = C.uint(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetDefaultEccMode
func nvmlDeviceGetDefaultEccMode(device C.nvmlDevice_t, defaultMode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetDefaultEccMode"); !ok {
		return ret
	}
	if defaultMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetDefaultEccMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*defaultMode = C.nvmlEnableState_t(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSupportedClocksThrottleReasons
func nvmlDeviceGetSupportedClocksThrottleReasons(device C.nvmlDevice_t, supportedClocksThrottleReasons *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetSupportedClocksThrottleReasons"); !ok {
		return ret
	}
	if supportedClocksThrottleReasons == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetSupportedClocksThrottleReasons()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*supportedClocksThrottleReasons = C.ulonglong(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAutoBoostedClocksEnabled
func nvmlDeviceGetAutoBoostedClocksEnabled(device C.nvmlDevice_t, isEnabled *C.nvmlEnableState_t, defaultIsEnabled *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAutoBoostedClocksEnabled"); !ok {
		return ret
	}
	if isEnabled == nil || defaultIsEnabled == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	enabled, defaultEnabled, ret := dev.GetAutoBoostedClocksEnabled()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*isEnabled = C.nvmlEnableState_t(enabled)
	*defaultIsEnabled = C.nvmlEnableState_t(defaultEnabled)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGspFirmwareVersion
func nvmlDeviceGetGspFirmwareVersion(device C.nvmlDevice_t, version *C.char) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGspFirmwareVersion"); !ok {
		return ret
	}
	if version == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetGspFirmwareVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(val, version, nvml.GSP_FIRMWARE_VERSION_BUF_SIZE)
}

//export nvmlDeviceGetTotalEnergyConsumption
func nvmlDeviceGetTotalEnergyConsumption(device C.nvmlDevice_t, energy *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTotalEnergyConsumption"); !ok {
		return ret
	}
	if energy == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	val, ret := dev.GetTotalEnergyConsumption()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*energy = C.ulonglong(val)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetDetailedEccErrors
func nvmlDeviceGetDetailedEccErrors(device C.nvmlDevice_t, errorType C.nvmlMemoryErrorType_t, counterType C.nvmlEccCounterType_t, eccCounts *C.nvmlEccErrorCounts_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetDetailedEccErrors"); !ok {
		return ret
	}
	if eccCounts == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev := engine.GetEngine().LookupConfigurableDevice(handle)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	counts, ret := dev.GetDetailedEccErrors(nvml.MemoryErrorType(errorType), nvml.EccCounterType(counterType))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	eccCounts.l1Cache = C.ulonglong(counts.L1Cache)
	eccCounts.l2Cache = C.ulonglong(counts.L2Cache)
	eccCounts.deviceMemory = C.ulonglong(counts.DeviceMemory)
	eccCounts.registerFile = C.ulonglong(counts.RegisterFile)
	return C.NVML_SUCCESS
}
