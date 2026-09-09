// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

// MIG exports: the GPU/compute instance lifecycle and the MIG device queries
// derived from it. The partitioning logic lives in the engine; this file only
// marshals between it and the C ABI.
//
// Two conventions recur:
//
//   - Array queries are two-phase. A nil array means "tell me the size", and
//     an array too small returns ERROR_INSUFFICIENT_SIZE with count set to
//     what is needed. Callers written against real NVML rely on this.
//   - Instance handles are their own opaque pointers, distinct from device
//     handles, and are invalidated when the instance is destroyed. A caller
//     that keeps using a destroyed instance's handle gets
//     ERROR_INVALID_ARGUMENT, as it would from the driver.

package main

/*
#include <stdlib.h>
#include <string.h>
#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// The version tags a caller stamps to ask for the v3 profile-info layouts.
// Both encode (size, version) the way NVML_STRUCT_VERSION does; see
// FabricStructVersion. mig_layout_test.go pins them against go-nvml's helper.
var (
	gpuInstanceProfileInfoV3Version = FabricStructVersion(
		C.sizeof_nvmlGpuInstanceProfileInfo_v3_t, 3)
	computeInstanceProfileInfoV3Version = FabricStructVersion(
		C.sizeof_nvmlComputeInstanceProfileInfo_v3_t, 3)
)

// =============================================================================
// MIG mode
// =============================================================================

// nvmlDeviceSetMigMode enables or disables MIG on a device.
//
// activationStatus is where real NVML reports that the mode change needs a GPU
// reset before it takes effect. The mock applies the change immediately, so it
// reports success there too.
//
//export nvmlDeviceSetMigMode
func nvmlDeviceSetMigMode(device C.nvmlDevice_t, mode C.uint, activationStatus *C.nvmlReturn_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetMigMode"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	ret, activation := dev.SetMigMode(int(mode))
	if activationStatus != nil {
		*activationStatus = toReturn(activation)
	}
	return toReturn(ret)
}

// =============================================================================
// GPU instance profiles
// =============================================================================

//export nvmlDeviceGetGpuInstanceProfileInfo
func nvmlDeviceGetGpuInstanceProfileInfo(
	device C.nvmlDevice_t, profile C.uint, info *C.nvmlGpuInstanceProfileInfo_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstanceProfileInfo"); !ok {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := engine.GetEngine().DeviceGetGpuInstanceProfileInfo(unsafe.Pointer(device.handle), int(profile))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	info.id = C.uint(got.Id)
	info.isP2pSupported = C.uint(got.IsP2pSupported)
	info.sliceCount = C.uint(got.SliceCount)
	info.instanceCount = C.uint(got.InstanceCount)
	info.multiprocessorCount = C.uint(got.MultiprocessorCount)
	info.copyEngineCount = C.uint(got.CopyEngineCount)
	info.decoderCount = C.uint(got.DecoderCount)
	info.encoderCount = C.uint(got.EncoderCount)
	info.jpegCount = C.uint(got.JpegCount)
	info.ofaCount = C.uint(got.OfaCount)
	info.memorySizeMB = C.ulonglong(got.MemorySizeMB)
	return C.NVML_SUCCESS
}

// nvmlDeviceGetGpuInstanceProfileInfoV is the versioned form. The caller
// stamps info.version and the bridge writes the matching layout; v3 drops
// isP2pSupported and appends capabilities, so it is not a superset of v2.
//
//export nvmlDeviceGetGpuInstanceProfileInfoV
func nvmlDeviceGetGpuInstanceProfileInfoV(
	device C.nvmlDevice_t, profile C.uint, info *C.nvmlGpuInstanceProfileInfo_v2_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstanceProfileInfoV"); !ok {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := engine.GetEngine().DeviceGetGpuInstanceProfileInfo(unsafe.Pointer(device.handle), int(profile))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// A profile the board offers but the mock cannot name still has usable
	// engine counts, so an unnameable profile leaves the name empty rather
	// than failing the whole query.
	name, _ := engine.GetEngine().DeviceGetGpuInstanceProfileName(unsafe.Pointer(device.handle), int(profile))
	return writeGpuInstanceProfileInfoV(info, got, name)
}

// nvmlDeviceGetGpuInstanceProfileInfoByIdV looks a profile up by its ID. The
// mock's profile IDs are the NVML profile constants, so this is the same query
// as by profile.
//
//export nvmlDeviceGetGpuInstanceProfileInfoByIdV
func nvmlDeviceGetGpuInstanceProfileInfoByIdV(
	device C.nvmlDevice_t, profileId C.uint, info *C.nvmlGpuInstanceProfileInfo_v2_t,
) C.nvmlReturn_t {
	return nvmlDeviceGetGpuInstanceProfileInfoV(device, profileId, info)
}

// writeGpuInstanceProfileInfoV fills either the v2 or the v3 layout, chosen by
// the version the caller stamped. A caller that stamps nothing gets v2, the
// layout the unversioned V entry point has always written.
func writeGpuInstanceProfileInfoV(
	info *C.nvmlGpuInstanceProfileInfo_v2_t, got nvml.GpuInstanceProfileInfo, name string,
) C.nvmlReturn_t {
	if uint32(info.version) == gpuInstanceProfileInfoV3Version {
		v3 := (*C.nvmlGpuInstanceProfileInfo_v3_t)(unsafe.Pointer(info))
		v3.id = C.uint(got.Id)
		v3.sliceCount = C.uint(got.SliceCount)
		v3.instanceCount = C.uint(got.InstanceCount)
		v3.multiprocessorCount = C.uint(got.MultiprocessorCount)
		v3.copyEngineCount = C.uint(got.CopyEngineCount)
		v3.decoderCount = C.uint(got.DecoderCount)
		v3.encoderCount = C.uint(got.EncoderCount)
		v3.jpegCount = C.uint(got.JpegCount)
		v3.ofaCount = C.uint(got.OfaCount)
		v3.memorySizeMB = C.ulonglong(got.MemorySizeMB)
		v3.capabilities = 0
		copyProfileName(&v3.name[0], len(v3.name), name)
		return C.NVML_SUCCESS
	}

	info.id = C.uint(got.Id)
	info.isP2pSupported = C.uint(got.IsP2pSupported)
	info.sliceCount = C.uint(got.SliceCount)
	info.instanceCount = C.uint(got.InstanceCount)
	info.multiprocessorCount = C.uint(got.MultiprocessorCount)
	info.copyEngineCount = C.uint(got.CopyEngineCount)
	info.decoderCount = C.uint(got.DecoderCount)
	info.encoderCount = C.uint(got.EncoderCount)
	info.jpegCount = C.uint(got.JpegCount)
	info.ofaCount = C.uint(got.OfaCount)
	info.memorySizeMB = C.ulonglong(got.MemorySizeMB)
	copyProfileName(&info.name[0], len(info.name), name)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGpuInstancePossiblePlacements
func nvmlDeviceGetGpuInstancePossiblePlacements(
	device C.nvmlDevice_t, profileId C.uint, placements *C.nvmlGpuInstancePlacement_t, count *C.uint,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstancePossiblePlacements"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := engine.GetEngine().DeviceGetGpuInstancePossiblePlacements(
		unsafe.Pointer(device.handle), int(profileId))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	if placements == nil {
		*count = C.uint(len(got))
		return C.NVML_SUCCESS
	}
	if int(*count) < len(got) {
		*count = C.uint(len(got))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(placements, len(got))
	for i, p := range got {
		out[i].start = C.uint(p.Start)
		out[i].size = C.uint(p.Size)
	}
	*count = C.uint(len(got))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGpuInstancePossiblePlacements_v2
func nvmlDeviceGetGpuInstancePossiblePlacements_v2(
	device C.nvmlDevice_t, profileId C.uint, placements *C.nvmlGpuInstancePlacement_t, count *C.uint,
) C.nvmlReturn_t {
	return nvmlDeviceGetGpuInstancePossiblePlacements(device, profileId, placements, count)
}

//export nvmlDeviceGetGpuInstanceRemainingCapacity
func nvmlDeviceGetGpuInstanceRemainingCapacity(
	device C.nvmlDevice_t, profileId C.uint, count *C.uint,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstanceRemainingCapacity"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	remaining, ret := engine.GetEngine().DeviceGetGpuInstanceRemainingCapacity(
		unsafe.Pointer(device.handle), int(profileId))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*count = C.uint(remaining)
	return C.NVML_SUCCESS
}

// =============================================================================
// GPU instance lifecycle
// =============================================================================

//export nvmlDeviceCreateGpuInstance
func nvmlDeviceCreateGpuInstance(
	device C.nvmlDevice_t, profileId C.uint, gpuInstance *C.nvmlGpuInstance_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceCreateGpuInstance"); !ok {
		return ret
	}
	if gpuInstance == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle, ret := engine.GetEngine().DeviceCreateGpuInstance(
		unsafe.Pointer(device.handle), int(profileId), nil)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*gpuInstance = C.nvmlGpuInstance_t(handle)
	return C.NVML_SUCCESS
}

//export nvmlDeviceCreateGpuInstanceWithPlacement
func nvmlDeviceCreateGpuInstanceWithPlacement(
	device C.nvmlDevice_t, profileId C.uint,
	placement *C.nvmlGpuInstancePlacement_t, gpuInstance *C.nvmlGpuInstance_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceCreateGpuInstanceWithPlacement"); !ok {
		return ret
	}
	if gpuInstance == nil || placement == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	want := nvml.GpuInstancePlacement{
		Start: uint32(placement.start),
		Size:  uint32(placement.size),
	}
	handle, ret := engine.GetEngine().DeviceCreateGpuInstance(
		unsafe.Pointer(device.handle), int(profileId), &want)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*gpuInstance = C.nvmlGpuInstance_t(handle)
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceDestroy
func nvmlGpuInstanceDestroy(gpuInstance C.nvmlGpuInstance_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceDestroy"); !ok {
		return ret
	}
	return toReturn(engine.GetEngine().GpuInstanceDestroy(unsafe.Pointer(gpuInstance)))
}

//export nvmlDeviceGetGpuInstances
func nvmlDeviceGetGpuInstances(
	device C.nvmlDevice_t, profileId C.uint, gpuInstances *C.nvmlGpuInstance_t, count *C.uint,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstances"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handles, ret := engine.GetEngine().DeviceGetGpuInstances(unsafe.Pointer(device.handle), int(profileId))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	if gpuInstances == nil {
		*count = C.uint(len(handles))
		return C.NVML_SUCCESS
	}
	if int(*count) < len(handles) {
		*count = C.uint(len(handles))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(gpuInstances, len(handles))
	for i, h := range handles {
		out[i] = C.nvmlGpuInstance_t(h)
	}
	*count = C.uint(len(handles))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGpuInstanceById
func nvmlDeviceGetGpuInstanceById(
	device C.nvmlDevice_t, id C.uint, gpuInstance *C.nvmlGpuInstance_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstanceById"); !ok {
		return ret
	}
	if gpuInstance == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle, ret := engine.GetEngine().DeviceGetGpuInstanceById(unsafe.Pointer(device.handle), int(id))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*gpuInstance = C.nvmlGpuInstance_t(handle)
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceGetInfo
func nvmlGpuInstanceGetInfo(gpuInstance C.nvmlGpuInstance_t, info *C.nvmlGpuInstanceInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetInfo"); !ok {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, deviceHandle, ret := engine.GetEngine().GpuInstanceGetInfo(unsafe.Pointer(gpuInstance))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	info.device.handle = (*C.struct_nvmlDevice_st)(deviceHandle)
	info.id = C.uint(got.Id)
	info.profileId = C.uint(got.ProfileId)
	info.placement.start = C.uint(got.Placement.Start)
	info.placement.size = C.uint(got.Placement.Size)
	return C.NVML_SUCCESS
}

// =============================================================================
// Compute instance profiles
// =============================================================================

//export nvmlGpuInstanceGetComputeInstanceProfileInfo
func nvmlGpuInstanceGetComputeInstanceProfileInfo(
	gpuInstance C.nvmlGpuInstance_t, profile C.uint, engProfile C.uint,
	info *C.nvmlComputeInstanceProfileInfo_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetComputeInstanceProfileInfo"); !ok {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := engine.GetEngine().GpuInstanceGetComputeInstanceProfileInfo(
		unsafe.Pointer(gpuInstance), int(profile), int(engProfile))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	info.id = C.uint(got.Id)
	info.sliceCount = C.uint(got.SliceCount)
	info.instanceCount = C.uint(got.InstanceCount)
	info.multiprocessorCount = C.uint(got.MultiprocessorCount)
	info.sharedCopyEngineCount = C.uint(got.SharedCopyEngineCount)
	info.sharedDecoderCount = C.uint(got.SharedDecoderCount)
	info.sharedEncoderCount = C.uint(got.SharedEncoderCount)
	info.sharedJpegCount = C.uint(got.SharedJpegCount)
	info.sharedOfaCount = C.uint(got.SharedOfaCount)
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceGetComputeInstanceProfileInfoV
func nvmlGpuInstanceGetComputeInstanceProfileInfoV(
	gpuInstance C.nvmlGpuInstance_t, profile C.uint, engProfile C.uint,
	info *C.nvmlComputeInstanceProfileInfo_v2_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetComputeInstanceProfileInfoV"); !ok {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := engine.GetEngine().GpuInstanceGetComputeInstanceProfileInfo(
		unsafe.Pointer(gpuInstance), int(profile), int(engProfile))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	name, _ := engine.GetEngine().GpuInstanceGetComputeInstanceProfileName(
		unsafe.Pointer(gpuInstance), int(profile))
	if uint32(info.version) == computeInstanceProfileInfoV3Version {
		v3 := (*C.nvmlComputeInstanceProfileInfo_v3_t)(unsafe.Pointer(info))
		v3.id = C.uint(got.Id)
		v3.sliceCount = C.uint(got.SliceCount)
		v3.instanceCount = C.uint(got.InstanceCount)
		v3.multiprocessorCount = C.uint(got.MultiprocessorCount)
		v3.sharedCopyEngineCount = C.uint(got.SharedCopyEngineCount)
		v3.sharedDecoderCount = C.uint(got.SharedDecoderCount)
		v3.sharedEncoderCount = C.uint(got.SharedEncoderCount)
		v3.sharedJpegCount = C.uint(got.SharedJpegCount)
		v3.sharedOfaCount = C.uint(got.SharedOfaCount)
		v3.capabilities = 0
		copyProfileName(&v3.name[0], len(v3.name), name)
		return C.NVML_SUCCESS
	}

	info.id = C.uint(got.Id)
	info.sliceCount = C.uint(got.SliceCount)
	info.instanceCount = C.uint(got.InstanceCount)
	info.multiprocessorCount = C.uint(got.MultiprocessorCount)
	info.sharedCopyEngineCount = C.uint(got.SharedCopyEngineCount)
	info.sharedDecoderCount = C.uint(got.SharedDecoderCount)
	info.sharedEncoderCount = C.uint(got.SharedEncoderCount)
	info.sharedJpegCount = C.uint(got.SharedJpegCount)
	info.sharedOfaCount = C.uint(got.SharedOfaCount)
	copyProfileName(&info.name[0], len(info.name), name)
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceGetComputeInstanceRemainingCapacity
func nvmlGpuInstanceGetComputeInstanceRemainingCapacity(
	gpuInstance C.nvmlGpuInstance_t, profileId C.uint, count *C.uint,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetComputeInstanceRemainingCapacity"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	remaining, ret := engine.GetEngine().GpuInstanceGetComputeInstanceRemainingCapacity(
		unsafe.Pointer(gpuInstance), int(profileId))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*count = C.uint(remaining)
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceGetComputeInstancePossiblePlacements
func nvmlGpuInstanceGetComputeInstancePossiblePlacements(
	gpuInstance C.nvmlGpuInstance_t, profileId C.uint,
	placements *C.nvmlComputeInstancePlacement_t, count *C.uint,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetComputeInstancePossiblePlacements"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := engine.GetEngine().GpuInstanceGetComputeInstancePossiblePlacements(
		unsafe.Pointer(gpuInstance), int(profileId))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	if placements == nil {
		*count = C.uint(len(got))
		return C.NVML_SUCCESS
	}
	if int(*count) < len(got) {
		*count = C.uint(len(got))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(placements, len(got))
	for i, p := range got {
		out[i].start = C.uint(p.Start)
		out[i].size = C.uint(p.Size)
	}
	*count = C.uint(len(got))
	return C.NVML_SUCCESS
}

// =============================================================================
// Compute instance lifecycle
// =============================================================================

//export nvmlGpuInstanceCreateComputeInstance
func nvmlGpuInstanceCreateComputeInstance(
	gpuInstance C.nvmlGpuInstance_t, profileId C.uint, computeInstance *C.nvmlComputeInstance_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceCreateComputeInstance"); !ok {
		return ret
	}
	if computeInstance == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle, ret := engine.GetEngine().GpuInstanceCreateComputeInstance(
		unsafe.Pointer(gpuInstance), int(profileId), nil)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*computeInstance = C.nvmlComputeInstance_t(handle)
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceCreateComputeInstanceWithPlacement
func nvmlGpuInstanceCreateComputeInstanceWithPlacement(
	gpuInstance C.nvmlGpuInstance_t, profileId C.uint,
	placement *C.nvmlComputeInstancePlacement_t, computeInstance *C.nvmlComputeInstance_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceCreateComputeInstanceWithPlacement"); !ok {
		return ret
	}
	if computeInstance == nil || placement == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	want := nvml.ComputeInstancePlacement{
		Start: uint32(placement.start),
		Size:  uint32(placement.size),
	}
	handle, ret := engine.GetEngine().GpuInstanceCreateComputeInstance(
		unsafe.Pointer(gpuInstance), int(profileId), &want)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*computeInstance = C.nvmlComputeInstance_t(handle)
	return C.NVML_SUCCESS
}

//export nvmlComputeInstanceDestroy
func nvmlComputeInstanceDestroy(computeInstance C.nvmlComputeInstance_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlComputeInstanceDestroy"); !ok {
		return ret
	}
	return toReturn(engine.GetEngine().ComputeInstanceDestroy(unsafe.Pointer(computeInstance)))
}

//export nvmlGpuInstanceGetComputeInstances
func nvmlGpuInstanceGetComputeInstances(
	gpuInstance C.nvmlGpuInstance_t, profileId C.uint,
	computeInstances *C.nvmlComputeInstance_t, count *C.uint,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetComputeInstances"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handles, ret := engine.GetEngine().GpuInstanceGetComputeInstances(
		unsafe.Pointer(gpuInstance), int(profileId))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	if computeInstances == nil {
		*count = C.uint(len(handles))
		return C.NVML_SUCCESS
	}
	if int(*count) < len(handles) {
		*count = C.uint(len(handles))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(computeInstances, len(handles))
	for i, h := range handles {
		out[i] = C.nvmlComputeInstance_t(h)
	}
	*count = C.uint(len(handles))
	return C.NVML_SUCCESS
}

//export nvmlGpuInstanceGetComputeInstanceById
func nvmlGpuInstanceGetComputeInstanceById(
	gpuInstance C.nvmlGpuInstance_t, id C.uint, computeInstance *C.nvmlComputeInstance_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlGpuInstanceGetComputeInstanceById"); !ok {
		return ret
	}
	if computeInstance == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle, ret := engine.GetEngine().GpuInstanceGetComputeInstanceById(unsafe.Pointer(gpuInstance), int(id))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*computeInstance = C.nvmlComputeInstance_t(handle)
	return C.NVML_SUCCESS
}

//export nvmlComputeInstanceGetInfo_v2
func nvmlComputeInstanceGetInfo_v2(
	computeInstance C.nvmlComputeInstance_t, info *C.nvmlComputeInstanceInfo_t,
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlComputeInstanceGetInfo_v2"); !ok {
		return ret
	}
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, deviceHandle, giHandle, ret := engine.GetEngine().ComputeInstanceGetInfo(unsafe.Pointer(computeInstance))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	info.device.handle = (*C.struct_nvmlDevice_st)(deviceHandle)
	info.gpuInstance = C.nvmlGpuInstance_t(giHandle)
	info.id = C.uint(got.Id)
	info.profileId = C.uint(got.ProfileId)
	info.placement.start = C.uint(got.Placement.Start)
	info.placement.size = C.uint(got.Placement.Size)
	return C.NVML_SUCCESS
}

//export nvmlComputeInstanceGetInfo
func nvmlComputeInstanceGetInfo(
	computeInstance C.nvmlComputeInstance_t, info *C.nvmlComputeInstanceInfo_t,
) C.nvmlReturn_t {
	return nvmlComputeInstanceGetInfo_v2(computeInstance, info)
}

// =============================================================================
// MIG device queries
// =============================================================================

// nvmlDeviceGetGpuInstanceId returns the GPU instance behind a MIG device.
// ERROR_NOT_SUPPORTED on a full GPU is what tells a caller it is not looking
// at a partition.
//
//export nvmlDeviceGetGpuInstanceId
func nvmlDeviceGetGpuInstanceId(device C.nvmlDevice_t, id *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGpuInstanceId"); !ok {
		return ret
	}
	if id == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetGpuInstanceId()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*id = C.uint(got)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetComputeInstanceId
func nvmlDeviceGetComputeInstanceId(device C.nvmlDevice_t, id *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetComputeInstanceId"); !ok {
		return ret
	}
	if id == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetComputeInstanceId()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*id = C.uint(got)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAttributes_v2
func nvmlDeviceGetAttributes_v2(device C.nvmlDevice_t, attributes *C.nvmlDeviceAttributes_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAttributes_v2"); !ok {
		return ret
	}
	if attributes == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetAttributes()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	attributes.multiprocessorCount = C.uint(got.MultiprocessorCount)
	attributes.sharedCopyEngineCount = C.uint(got.SharedCopyEngineCount)
	attributes.sharedDecoderCount = C.uint(got.SharedDecoderCount)
	attributes.sharedEncoderCount = C.uint(got.SharedEncoderCount)
	attributes.sharedJpegCount = C.uint(got.SharedJpegCount)
	attributes.sharedOfaCount = C.uint(got.SharedOfaCount)
	attributes.gpuInstanceSliceCount = C.uint(got.GpuInstanceSliceCount)
	attributes.computeInstanceSliceCount = C.uint(got.ComputeInstanceSliceCount)
	attributes.memorySizeMB = C.ulonglong(got.MemorySizeMB)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAttributes
func nvmlDeviceGetAttributes(device C.nvmlDevice_t, attributes *C.nvmlDeviceAttributes_t) C.nvmlReturn_t {
	return nvmlDeviceGetAttributes_v2(device, attributes)
}

// copyProfileName writes a MIG profile name into a fixed C buffer, always
// NUL-terminated. The name is what nvidia-smi prints for a partition, and
// truncating rather than overflowing keeps a long name from corrupting the
// caller's struct.
func copyProfileName(dst *C.char, size int, name string) {
	if dst == nil || size <= 0 {
		return
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(dst)), size)
	clear(buf)

	n := copy(buf[:size-1], name)
	buf[n] = 0
}
