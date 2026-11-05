// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML power source, PowerMizer and power smoothing
// entry points, which back the "Power Smoothing" block of `nvidia-smi -q` and
// the `nvidia-smi power-smoothing` subcommand. The block's readings come from
// field ids served by nvmlDeviceGetFieldValues; these entry points write them.
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

func powerSmoothingProfileVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlPowerSmoothingProfile_t{}), 1)
}

//export nvmlDeviceGetPowerSource
func nvmlDeviceGetPowerSource(device C.nvmlDevice_t, powerSource *C.nvmlPowerSource_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerSource"); !ok {
		return ret
	}
	if powerSource == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	source, ret := dev.GetPowerSource()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*powerSource = C.nvmlPowerSource_t(source)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetPowerMizerMode_v1
func nvmlDeviceGetPowerMizerMode_v1(
	device C.nvmlDevice_t, powerMizerMode *C.nvmlDevicePowerMizerModes_v1_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPowerMizerMode_v1"); !ok {
		return ret
	}
	if powerMizerMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	modes, ret := dev.GetPowerMizerMode_v1()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// mode is the setter's input; the getter leaves the caller's value alone.
	powerMizerMode.currentMode = C.uint(modes.CurrentMode)
	powerMizerMode.supportedPowerMizerModes = C.uint(modes.SupportedPowerMizerModes)
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetPowerMizerMode_v1
func nvmlDeviceSetPowerMizerMode_v1(
	device C.nvmlDevice_t, powerMizerMode *C.nvmlDevicePowerMizerModes_v1_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetPowerMizerMode_v1"); !ok {
		return ret
	}
	if powerMizerMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetPowerMizerMode_v1(&nvml.DevicePowerMizerModes_v1{
		CurrentMode:              uint32(powerMizerMode.currentMode),
		Mode:                     uint32(powerMizerMode.mode),
		SupportedPowerMizerModes: uint32(powerMizerMode.supportedPowerMizerModes),
	}))
}

//export nvmlDevicePowerSmoothingSetState
func nvmlDevicePowerSmoothingSetState(device C.nvmlDevice_t, state *C.nvmlPowerSmoothingState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDevicePowerSmoothingSetState"); !ok {
		return ret
	}
	if state == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The struct is only read and upstream documents no version mismatch for
	// this entry point, so the tag is not checked.
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.PowerSmoothingSetState(&nvml.PowerSmoothingState{
		Version: uint32(state.version),
		State:   uint32(state.state),
	}))
}

//export nvmlDevicePowerSmoothingActivatePresetProfile
func nvmlDevicePowerSmoothingActivatePresetProfile(
	device C.nvmlDevice_t, profile *C.nvmlPowerSmoothingProfile_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDevicePowerSmoothingActivatePresetProfile"); !ok {
		return ret
	}
	if profile == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Read-only and undocumented for version mismatch, like SetState. Only
	// profileId is meaningful: nvidia-smi -spp leaves paramId and value
	// uninitialised.
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.PowerSmoothingActivatePresetProfile(readPowerSmoothingProfile(profile)))
}

//export nvmlDevicePowerSmoothingUpdatePresetProfileParam
func nvmlDevicePowerSmoothingUpdatePresetProfileParam(
	device C.nvmlDevice_t, profile *C.nvmlPowerSmoothingProfile_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDevicePowerSmoothingUpdatePresetProfileParam"); !ok {
		return ret
	}
	if profile == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Upstream documents ARGUMENT_VERSION_MISMATCH here, so a tag that is
	// present and wrong is refused. An unstamped zero is accepted because
	// go-nvml callers pass the struct through without filling it in.
	if v := uint32(profile.version); v != 0 && v != powerSmoothingProfileVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.PowerSmoothingUpdatePresetProfileParam(readPowerSmoothingProfile(profile)))
}

func readPowerSmoothingProfile(profile *C.nvmlPowerSmoothingProfile_t) *nvml.PowerSmoothingProfile {
	return &nvml.PowerSmoothingProfile{
		Version:   uint32(profile.version),
		ProfileId: uint32(profile.profileId),
		ParamId:   uint32(profile.paramId),
		Value:     float64(profile.value),
	}
}
