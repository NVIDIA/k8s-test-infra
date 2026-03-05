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
