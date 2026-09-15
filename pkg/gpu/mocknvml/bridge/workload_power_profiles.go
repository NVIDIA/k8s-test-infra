// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML workload power profile getters, which back
// `nvidia-smi power-profiles -l / -ld / -gr / -ge`. While they were generated
// stubs the whole subcommand answered "Workload Power Profiles feature is not
// supported on this device" on every profile, including Blackwell.
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

// Version tags a caller must stamp before these getters will write into its
// allocation. Unlike the power limit setters, these structs are written, not
// read, so an exact match is what keeps the writes inside the caller's buffer —
// and upstream documents ARGUMENT_VERSION_MISMATCH for both entry points.
func workloadProfilesInfoVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlWorkloadPowerProfileProfilesInfo_t{}), 1)
}

func workloadCurrentProfilesVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlWorkloadPowerProfileCurrentProfiles_t{}), 1)
}

//export nvmlDeviceWorkloadPowerProfileGetProfilesInfo
func nvmlDeviceWorkloadPowerProfileGetProfilesInfo(
	device C.nvmlDevice_t, profilesInfo *C.nvmlWorkloadPowerProfileProfilesInfo_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceWorkloadPowerProfileGetProfilesInfo"); !ok {
		return ret
	}
	if profilesInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(profilesInfo.version) != workloadProfilesInfoVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	writeMask255(&profilesInfo.perfProfilesMask, info.PerfProfilesMask)
	// Walk the whole array rather than stopping at the last supported
	// profile: the caller's buffer is uninitialised, and leaving the tail
	// untouched is what made nvidia-smi render hundreds of invented profiles
	// in issue #636's class of bug.
	for i := range int(C.NVML_WORKLOAD_POWER_MAX_PROFILES) {
		entry := &profilesInfo.perfProfile[i]
		entry.version = C.uint(info.PerfProfile[i].Version)
		entry.profileId = C.uint(info.PerfProfile[i].ProfileId)
		entry.priority = C.uint(info.PerfProfile[i].Priority)
		writeMask255(&entry.conflictingMask, info.PerfProfile[i].ConflictingMask)
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceWorkloadPowerProfileGetCurrentProfiles
func nvmlDeviceWorkloadPowerProfileGetCurrentProfiles(
	device C.nvmlDevice_t, currentProfiles *C.nvmlWorkloadPowerProfileCurrentProfiles_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceWorkloadPowerProfileGetCurrentProfiles"); !ok {
		return ret
	}
	if currentProfiles == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(currentProfiles.version) != workloadCurrentProfilesVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	current, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	writeMask255(&currentProfiles.perfProfilesMask, current.PerfProfilesMask)
	writeMask255(&currentProfiles.requestedProfilesMask, current.RequestedProfilesMask)
	writeMask255(&currentProfiles.enforcedProfilesMask, current.EnforcedProfilesMask)
	return C.NVML_SUCCESS
}

// requestedProfilesVersion is the tag a caller stamps into the struct the two
// deprecated setters take.
func requestedProfilesVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlWorkloadPowerProfileRequestedProfiles_t{}), 1)
}

//export nvmlDeviceWorkloadPowerProfileSetRequestedProfiles
func nvmlDeviceWorkloadPowerProfileSetRequestedProfiles(
	device C.nvmlDevice_t, requestedProfiles *C.nvmlWorkloadPowerProfileRequestedProfiles_t,
) C.nvmlReturn_t {
	return updateRequestedProfiles("nvmlDeviceWorkloadPowerProfileSetRequestedProfiles",
		device, requestedProfiles, nvml.POWER_PROFILE_OPERATION_SET)
}

//export nvmlDeviceWorkloadPowerProfileClearRequestedProfiles
func nvmlDeviceWorkloadPowerProfileClearRequestedProfiles(
	device C.nvmlDevice_t, requestedProfiles *C.nvmlWorkloadPowerProfileRequestedProfiles_t,
) C.nvmlReturn_t {
	return updateRequestedProfiles("nvmlDeviceWorkloadPowerProfileClearRequestedProfiles",
		device, requestedProfiles, nvml.POWER_PROFILE_OPERATION_CLEAR)
}

// updateRequestedProfiles is the shared body of the two deprecated setters,
// which differ only in the operation they imply. nvidia-smi 580 still calls
// these rather than UpdateProfiles_v1, so `-sr` and `-cr` depend on them.
func updateRequestedProfiles(
	funcName string, device C.nvmlDevice_t,
	requestedProfiles *C.nvmlWorkloadPowerProfileRequestedProfiles_t, op nvml.PowerProfileOperation,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck(funcName); !ok {
		return ret
	}
	if requestedProfiles == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The struct is read, not written, so a wrong tag cannot overrun the
	// caller's buffer — but upstream documents ARGUMENT_VERSION_MISMATCH for
	// both entry points, so a tag that is present and wrong is refused as
	// such. An unstamped zero is accepted because go-nvml callers pass the
	// struct through without filling it in.
	if v := uint32(requestedProfiles.version); v != 0 && v != requestedProfilesVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.WorkloadPowerProfileUpdateProfiles(op, readMask255(&requestedProfiles.requestedProfilesMask)))
}

//export nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1
func nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1(
	device C.nvmlDevice_t, updateProfiles *C.nvmlWorkloadPowerProfileUpdateProfiles_v1_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1"); !ok {
		return ret
	}
	if updateProfiles == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// No version tag to check: this struct carries no version member, which
	// is also why upstream does not document ARGUMENT_VERSION_MISMATCH here.
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.WorkloadPowerProfileUpdateProfiles(
		nvml.PowerProfileOperation(updateProfiles.operation),
		readMask255(&updateProfiles.updateProfilesMask)))
}

// writeMask255 copies a mask word by word. The Go and C arrays have identical
// element counts and widths, but they are distinct types, so the copy is
// explicit rather than a cast.
func writeMask255(dst *C.nvmlMask255_t, src nvml.Mask255) {
	for i := range int(C.NVML_255_MASK_NUM_ELEMS) {
		dst.mask[i] = C.uint(src.Mask[i])
	}
}

// readMask255 is writeMask255's inverse, for the masks a caller passes in.
func readMask255(src *C.nvmlMask255_t) nvml.Mask255 {
	var out nvml.Mask255
	for i := range int(C.NVML_255_MASK_NUM_ELEMS) {
		out.Mask[i] = uint32(src.mask[i])
	}
	return out
}
