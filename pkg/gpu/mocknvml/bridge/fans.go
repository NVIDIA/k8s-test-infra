// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML fan and cooling entry points beyond the
// board-wide duty-cycle reading in device.go, plus the S-class unit
// enumeration that decides whether the unit fan reading is reachable at all.
//
// nvidia-smi reaches only two of these: nvmlUnitGetFanSpeedInfo, via the unit
// enumeration below that `-q -u` walks, and nvmlDeviceGetFanSpeed, which was
// already implemented. The rest answer a caller holding NVML directly, so
// tests/mocknvml is where they are covered.
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

// Version tags a caller must stamp before these two getters will write into its
// allocation. Both structs are written, so an exact match is what keeps the
// writes inside the caller's buffer; go-nvml's own bindings stamp them.
func fanSpeedInfoVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlFanSpeedInfo_t{}), 1)
}

func coolerInfoVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlCoolerInfo_t{}), 1)
}

// lookupFanDevice resolves the guards the device-level entry points share. A
// nil device means the caller may not go on.
func lookupFanDevice(device C.nvmlDevice_t) *engine.ConfigurableDevice {
	return engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
}

//export nvmlDeviceGetFanSpeedRPM
func nvmlDeviceGetFanSpeedRPM(device C.nvmlDevice_t, fanSpeed *C.nvmlFanSpeedInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetFanSpeedRPM"); !ok {
		return ret
	}
	if fanSpeed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(fanSpeed.version) != fanSpeedInfoVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The fan index is an input carried inside the struct rather than an
	// argument, so it is read back out here. go-nvml's binding leaves it
	// zero, which asks about the board's first fan.
	rpm, ret := dev.FanSpeedRPM(int(fanSpeed.fan))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	fanSpeed.speed = C.uint(rpm)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCoolerInfo
func nvmlDeviceGetCoolerInfo(device C.nvmlDevice_t, coolerInfo *C.nvmlCoolerInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCoolerInfo"); !ok {
		return ret
	}
	if coolerInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(coolerInfo.version) != coolerInfoVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.CoolerInfo(int(coolerInfo.index))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	coolerInfo.signalType = C.uint(info.SignalType)
	coolerInfo.target = C.uint(info.Target)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetTargetFanSpeed
func nvmlDeviceGetTargetFanSpeed(device C.nvmlDevice_t, fan C.uint, targetSpeed *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTargetFanSpeed"); !ok {
		return ret
	}
	if targetSpeed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	target, ret := dev.GetTargetFanSpeed(int(fan))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*targetSpeed = C.uint(target)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetMinMaxFanSpeed
func nvmlDeviceGetMinMaxFanSpeed(device C.nvmlDevice_t, minSpeed *C.uint, maxSpeed *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMinMaxFanSpeed"); !ok {
		return ret
	}
	if minSpeed == nil || maxSpeed == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	lo, hi, ret := dev.GetMinMaxFanSpeed()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*minSpeed = C.uint(lo)
	*maxSpeed = C.uint(hi)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetFanControlPolicy_v2
func nvmlDeviceGetFanControlPolicy_v2(
	device C.nvmlDevice_t, fan C.uint, policy *C.nvmlFanControlPolicy_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetFanControlPolicy_v2"); !ok {
		return ret
	}
	if policy == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	current, ret := dev.GetFanControlPolicy_v2(int(fan))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*policy = C.nvmlFanControlPolicy_t(current)
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetFanControlPolicy
func nvmlDeviceSetFanControlPolicy(
	device C.nvmlDevice_t, fan C.uint, policy C.nvmlFanControlPolicy_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetFanControlPolicy"); !ok {
		return ret
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetFanControlPolicy(int(fan), nvml.FanControlPolicy(policy)))
}

//export nvmlDeviceSetFanSpeed_v2
func nvmlDeviceSetFanSpeed_v2(device C.nvmlDevice_t, fan C.uint, speed C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetFanSpeed_v2"); !ok {
		return ret
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetFanSpeed_v2(int(fan), int(speed)))
}

//export nvmlDeviceSetDefaultFanSpeed_v2
func nvmlDeviceSetDefaultFanSpeed_v2(device C.nvmlDevice_t, fan C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetDefaultFanSpeed_v2"); !ok {
		return ret
	}
	dev := lookupFanDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetDefaultFanSpeed_v2(int(fan)))
}

// S-class unit enumeration.
//
// Units are the chassis management processors of the S870/S1070 generation.
// Nothing Mokka models has one, and nor does any GPU a modern driver sees, so
// the honest answer is an empty enumeration: a count of zero, and a refusal for
// any index. nvidia-smi calls nvmlUnitGetCount before it touches a unit, so
// answering zero is what makes `-q -u` report no units rather than fail — the
// difference between a node with no S-class chassis and a broken driver.
//
// Enumerating nothing is also what makes the unit fan reading below correct
// rather than merely unreachable: a caller can only ever hold a handle NVML
// itself would refuse to hand out.

//export nvmlUnitGetCount
func nvmlUnitGetCount(unitCount *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlUnitGetCount"); !ok {
		return ret
	}
	if unitCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	*unitCount = 0
	debugLog("[NVML] nvmlUnitGetCount -> 0 (no S-class units)\n")
	return C.NVML_SUCCESS
}

//export nvmlUnitGetHandleByIndex
func nvmlUnitGetHandleByIndex(index C.uint, unit *C.nvmlUnit_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlUnitGetHandleByIndex"); !ok {
		return ret
	}
	if unit == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Every index is out of range against a count of zero, which upstream
	// documents as INVALID_ARGUMENT.
	debugLog("[NVML] nvmlUnitGetHandleByIndex(%d) -> INVALID_ARGUMENT (no S-class units)\n", uint32(index))
	return C.NVML_ERROR_INVALID_ARGUMENT
}

//export nvmlUnitGetFanSpeedInfo
func nvmlUnitGetFanSpeedInfo(unit C.nvmlUnit_t, fanSpeeds *C.nvmlUnitFanSpeeds_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlUnitGetFanSpeedInfo"); !ok {
		return ret
	}
	if fanSpeeds == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// No handle can be valid, since none is ever handed out. The buffer is
	// left untouched: a caller that ignores the return must not find a
	// plausible-looking fan count in it. The handle is logged because a
	// non-nil one means a consumer fabricated it rather than enumerating.
	debugLog("[NVML] nvmlUnitGetFanSpeedInfo(unit=%p) -> INVALID_ARGUMENT (no S-class units)\n",
		unsafe.Pointer(unit))
	return C.NVML_ERROR_INVALID_ARGUMENT
}
