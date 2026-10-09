// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML supported-clock tables, performance states and
// clock tuning functions. All 25 were generated stubs: `nvidia-smi -q -d
// SUPPORTED_CLOCKS` printed N/A on every profile although each one already
// declared a table, and `-ac`, `-lgc` and `-lmc` were refused.
package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func clockOffsetVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlClockOffset_t{}), 1)
}

func perfModesVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlDevicePerfModes_t{}), 1)
}

func currentClockFreqsVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlDeviceCurrentClockFreqs_t{}), 1)
}

// lookupClockDevice resolves a handle after the driver-version gate, the
// preamble every function in this file shares.
func lookupClockDevice(name string, device C.nvmlDevice_t) (*engine.ConfigurableDevice, C.nvmlReturn_t) {
	if ret, ok := bridgeVersionCheck(name); !ok {
		return nil, ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return nil, C.NVML_ERROR_INVALID_ARGUMENT
	}
	return dev, C.NVML_SUCCESS
}

// fillClockList is the count-in/count-out contract both supported-clock
// getters share: *count is the caller's capacity, and is always set to the
// number of clocks so a caller that probed with 0 can allocate and call again.
// Nothing is written unless every clock fits, which is what keeps a short
// buffer — go-nvml passes a single element — from being overrun.
func fillClockList(clocks []uint32, count *C.uint, clocksMHz *C.uint) C.nvmlReturn_t {
	capacity := int(*count)
	*count = C.uint(len(clocks))
	if len(clocks) > capacity {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	if len(clocks) == 0 {
		return C.NVML_SUCCESS
	}
	if clocksMHz == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	out := unsafe.Slice(clocksMHz, len(clocks))
	for i, mhz := range clocks {
		out[i] = C.uint(mhz)
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSupportedMemoryClocks
func nvmlDeviceGetSupportedMemoryClocks(device C.nvmlDevice_t, count *C.uint, clocksMHz *C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetSupportedMemoryClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	clocks, r := dev.SupportedMemoryClocks()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	return fillClockList(clocks, count, clocksMHz)
}

//export nvmlDeviceGetSupportedGraphicsClocks
func nvmlDeviceGetSupportedGraphicsClocks(device C.nvmlDevice_t, memoryClockMHz C.uint, count *C.uint, clocksMHz *C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetSupportedGraphicsClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	clocks, r := dev.SupportedGraphicsClocks(uint32(memoryClockMHz))
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	return fillClockList(clocks, count, clocksMHz)
}

// nvmlDeviceGetSupportedPerformanceStates fills a fixed array, padding the
// slots after the supported states with NVML_PSTATE_UNKNOWN. nvml.h calls size
// a byte count, but go-nvml passes the element count, so it is read as
// elements and capped at NVML_MAX_GPU_PERF_PSTATES — the most a caller sizing
// it either way can have allocated.
//
//export nvmlDeviceGetSupportedPerformanceStates
func nvmlDeviceGetSupportedPerformanceStates(device C.nvmlDevice_t, pstates *C.nvmlPstates_t, size C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetSupportedPerformanceStates", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if pstates == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	states, r := dev.GetSupportedPerformanceStates()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	slots := min(int(size), nvml.MAX_GPU_PERF_PSTATES)
	if len(states) > slots {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	out := unsafe.Slice(pstates, slots)
	for i := range out {
		out[i] = C.nvmlPstates_t(nvml.PSTATE_UNKNOWN)
		if i < len(states) {
			out[i] = C.nvmlPstates_t(states[i])
		}
	}
	return C.NVML_SUCCESS
}

// nvmlDeviceGetMinMaxClockOfPState accepts one NULL output, as nvml.h only
// rejects both being NULL.
//
//export nvmlDeviceGetMinMaxClockOfPState
func nvmlDeviceGetMinMaxClockOfPState(device C.nvmlDevice_t, _type C.nvmlClockType_t, pstate C.nvmlPstates_t, minClockMHz *C.uint, maxClockMHz *C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetMinMaxClockOfPState", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if minClockMHz == nil && maxClockMHz == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	lo, hi, r := dev.GetMinMaxClockOfPState(nvml.ClockType(_type), nvml.Pstates(pstate))
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	if minClockMHz != nil {
		*minClockMHz = C.uint(lo)
	}
	if maxClockMHz != nil {
		*maxClockMHz = C.uint(hi)
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCurrentClockFreqs
func nvmlDeviceGetCurrentClockFreqs(device C.nvmlDevice_t, currentClockFreqs *C.nvmlDeviceCurrentClockFreqs_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetCurrentClockFreqs", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if currentClockFreqs == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(currentClockFreqs.version) != currentClockFreqsVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	s, r := dev.CurrentClockFreqs()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	return goStringToC(s, &currentClockFreqs.str[0], C.NVML_PERF_MODES_BUFFER_SIZE)
}

//export nvmlDeviceGetPerformanceModes
func nvmlDeviceGetPerformanceModes(device C.nvmlDevice_t, perfModes *C.nvmlDevicePerfModes_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetPerformanceModes", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if perfModes == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(perfModes.version) != perfModesVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	s, r := dev.PerformanceModes()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	return goStringToC(s, &perfModes.str[0], C.NVML_PERF_MODES_BUFFER_SIZE)
}

//export nvmlDeviceGetDynamicPstatesInfo
func nvmlDeviceGetDynamicPstatesInfo(device C.nvmlDevice_t, pDynamicPstatesInfo *C.nvmlGpuDynamicPstatesInfo_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetDynamicPstatesInfo", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if pDynamicPstatesInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, r := dev.GetDynamicPstatesInfo()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	pDynamicPstatesInfo.flags = C.uint(info.Flags)
	for i, u := range info.Utilization {
		pDynamicPstatesInfo.utilization[i] = C.nvmlGpuDynamicPstatesInfoUtilization_t{
			bIsPresent:   C.uint(u.BIsPresent),
			percentage:   C.uint(u.Percentage),
			incThreshold: C.uint(u.IncThreshold),
			decThreshold: C.uint(u.DecThreshold),
		}
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAdaptiveClockInfoStatus
func nvmlDeviceGetAdaptiveClockInfoStatus(device C.nvmlDevice_t, adaptiveClockStatus *C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetAdaptiveClockInfoStatus", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if adaptiveClockStatus == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	status, r := dev.GetAdaptiveClockInfoStatus()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*adaptiveClockStatus = C.uint(status)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSupportedClocksEventReasons
func nvmlDeviceGetSupportedClocksEventReasons(device C.nvmlDevice_t, supportedClocksEventReasons *C.ulonglong) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetSupportedClocksEventReasons", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if supportedClocksEventReasons == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	reasons, r := dev.GetSupportedClocksEventReasons()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*supportedClocksEventReasons = C.ulonglong(reasons)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetClockOffsets
func nvmlDeviceGetClockOffsets(device C.nvmlDevice_t, info *C.nvmlClockOffset_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetClockOffsets", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(info.version) != clockOffsetVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	off, r := dev.ClockOffset(nvml.ClockType(info._type), nvml.Pstates(info.pstate))
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	info.clockOffsetMHz = C.int(off.ClockOffsetMHz)
	info.minClockOffsetMHz = C.int(off.MinClockOffsetMHz)
	info.maxClockOffsetMHz = C.int(off.MaxClockOffsetMHz)
	return C.NVML_SUCCESS
}

// nvmlDeviceSetClockOffsets only reads the struct, so, as for the power setter,
// an unstamped zero version is accepted for go-nvml callers that pass the
// struct through without filling it in, while a wrong tag is refused.
//
//export nvmlDeviceSetClockOffsets
func nvmlDeviceSetClockOffsets(device C.nvmlDevice_t, info *C.nvmlClockOffset_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetClockOffsets", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if v := uint32(info.version); v != 0 && v != clockOffsetVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	return toReturn(dev.SetClockOffsets(nvml.ClockOffset{
		Version:        uint32(info.version),
		Type:           uint32(info._type),
		Pstate:         uint32(info.pstate),
		ClockOffsetMHz: int32(info.clockOffsetMHz),
	}))
}

//export nvmlDeviceGetGpcClkVfOffset
func nvmlDeviceGetGpcClkVfOffset(device C.nvmlDevice_t, offset *C.int) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetGpcClkVfOffset", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if offset == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	v, r := dev.GetGpcClkVfOffset()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*offset = C.int(v)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMemClkVfOffset
func nvmlDeviceGetMemClkVfOffset(device C.nvmlDevice_t, offset *C.int) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetMemClkVfOffset", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if offset == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	v, r := dev.GetMemClkVfOffset()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*offset = C.int(v)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGpcClkMinMaxVfOffset
func nvmlDeviceGetGpcClkMinMaxVfOffset(device C.nvmlDevice_t, minOffset *C.int, maxOffset *C.int) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetGpcClkMinMaxVfOffset", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if minOffset == nil || maxOffset == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	lo, hi, r := dev.GetGpcClkMinMaxVfOffset()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*minOffset, *maxOffset = C.int(lo), C.int(hi)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMemClkMinMaxVfOffset
func nvmlDeviceGetMemClkMinMaxVfOffset(device C.nvmlDevice_t, minOffset *C.int, maxOffset *C.int) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceGetMemClkMinMaxVfOffset", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if minOffset == nil || maxOffset == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	lo, hi, r := dev.GetMemClkMinMaxVfOffset()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*minOffset, *maxOffset = C.int(lo), C.int(hi)
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetGpcClkVfOffset
func nvmlDeviceSetGpcClkVfOffset(device C.nvmlDevice_t, offset C.int) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetGpcClkVfOffset", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetGpcClkVfOffset(int(offset)))
}

//export nvmlDeviceSetMemClkVfOffset
func nvmlDeviceSetMemClkVfOffset(device C.nvmlDevice_t, offset C.int) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetMemClkVfOffset", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetMemClkVfOffset(int(offset)))
}

//export nvmlDeviceSetApplicationsClocks
func nvmlDeviceSetApplicationsClocks(device C.nvmlDevice_t, memClockMHz C.uint, graphicsClockMHz C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetApplicationsClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetApplicationsClocks(uint32(memClockMHz), uint32(graphicsClockMHz)))
}

//export nvmlDeviceResetApplicationsClocks
func nvmlDeviceResetApplicationsClocks(device C.nvmlDevice_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceResetApplicationsClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.ResetApplicationsClocks())
}

//export nvmlDeviceSetGpuLockedClocks
func nvmlDeviceSetGpuLockedClocks(device C.nvmlDevice_t, minGpuClockMHz C.uint, maxGpuClockMHz C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetGpuLockedClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetGpuLockedClocks(uint32(minGpuClockMHz), uint32(maxGpuClockMHz)))
}

//export nvmlDeviceResetGpuLockedClocks
func nvmlDeviceResetGpuLockedClocks(device C.nvmlDevice_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceResetGpuLockedClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.ResetGpuLockedClocks())
}

//export nvmlDeviceSetMemoryLockedClocks
func nvmlDeviceSetMemoryLockedClocks(device C.nvmlDevice_t, minMemClockMHz C.uint, maxMemClockMHz C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetMemoryLockedClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetMemoryLockedClocks(uint32(minMemClockMHz), uint32(maxMemClockMHz)))
}

//export nvmlDeviceResetMemoryLockedClocks
func nvmlDeviceResetMemoryLockedClocks(device C.nvmlDevice_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceResetMemoryLockedClocks", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.ResetMemoryLockedClocks())
}

//export nvmlDeviceSetAutoBoostedClocksEnabled
func nvmlDeviceSetAutoBoostedClocksEnabled(device C.nvmlDevice_t, enabled C.nvmlEnableState_t) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetAutoBoostedClocksEnabled", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetAutoBoostedClocksEnabled(nvml.EnableState(enabled)))
}

//export nvmlDeviceSetDefaultAutoBoostedClocksEnabled
func nvmlDeviceSetDefaultAutoBoostedClocksEnabled(device C.nvmlDevice_t, enabled C.nvmlEnableState_t, flags C.uint) C.nvmlReturn_t {
	dev, ret := lookupClockDevice("nvmlDeviceSetDefaultAutoBoostedClocksEnabled", device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(dev.SetDefaultAutoBoostedClocksEnabled(nvml.EnableState(enabled), uint32(flags)))
}
