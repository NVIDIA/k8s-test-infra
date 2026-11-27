// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

/*
#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Go tests cannot import C. These adapters call the exports with their actual
// C argument types, keeping version dispatch and profile-ID translation covered.
func migProfileInfoForTest(handle unsafe.Pointer, profile, version int, byID bool) (nvml.GpuInstanceProfileInfo, string, nvml.Return) {
	device := C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
	if version == 1 {
		var info C.nvmlGpuInstanceProfileInfo_t
		ret := nvmlDeviceGetGpuInstanceProfileInfo(device, C.uint(profile), &info)
		return *(*nvml.GpuInstanceProfileInfo)(unsafe.Pointer(&info)), "", nvml.Return(ret)
	}
	var v2 C.nvmlGpuInstanceProfileInfo_v2_t
	var v3 C.nvmlGpuInstanceProfileInfo_v3_t
	v2.version = C.uint(FabricStructVersion(C.sizeof_nvmlGpuInstanceProfileInfo_v2_t, 2))
	info := &v2
	if version == 3 {
		v3.version = C.uint(gpuInstanceProfileInfoV3Version)
		info = (*C.nvmlGpuInstanceProfileInfo_v2_t)(unsafe.Pointer(&v3))
	}
	var ret C.nvmlReturn_t
	if byID {
		ret = nvmlDeviceGetGpuInstanceProfileInfoByIdV(device, C.uint(profile), info)
	} else {
		ret = nvmlDeviceGetGpuInstanceProfileInfoV(device, C.uint(profile), info)
	}
	if version == 3 {
		return nvml.GpuInstanceProfileInfo{Id: uint32(v3.id), InstanceCount: uint32(v3.instanceCount), MemorySizeMB: uint64(v3.memorySizeMB)}, goStringFromC(&v3.name[0], len(v3.name)), nvml.Return(ret)
	}
	return nvml.GpuInstanceProfileInfo{Id: uint32(v2.id), InstanceCount: uint32(v2.instanceCount), MemorySizeMB: uint64(v2.memorySizeMB)}, goStringFromC(&v2.name[0], len(v2.name)), nvml.Return(ret)
}

func migPlacementsForTest(handle unsafe.Pointer, profile, version int) ([]nvml.GpuInstancePlacement, nvml.Return) {
	device := C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
	query := nvmlDeviceGetGpuInstancePossiblePlacements
	if version == 1 {
		query = nvmlDeviceGetGpuInstancePossiblePlacements_v1
	}
	if version == 2 {
		query = nvmlDeviceGetGpuInstancePossiblePlacements_v2
	}
	var count C.uint
	ret := query(device, C.uint(profile), nil, &count)
	if ret != C.NVML_SUCCESS || count == 0 {
		return nil, nvml.Return(ret)
	}
	out := make([]C.nvmlGpuInstancePlacement_t, int(count))
	ret = query(device, C.uint(profile), &out[0], &count)
	placements := make([]nvml.GpuInstancePlacement, len(out))
	for i, p := range out {
		placements[i] = nvml.GpuInstancePlacement{Start: uint32(p.start), Size: uint32(p.size)}
	}
	return placements, nvml.Return(ret)
}

func migCapacityForTest(handle unsafe.Pointer, profile int) (int, nvml.Return) {
	device := C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
	var count C.uint
	ret := nvmlDeviceGetGpuInstanceRemainingCapacity(device, C.uint(profile), &count)
	return int(count), nvml.Return(ret)
}

func migCreateForTest(handle unsafe.Pointer, profile int, placement *nvml.GpuInstancePlacement) nvml.Return {
	device := C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
	var gi C.nvmlGpuInstance_t
	if placement == nil {
		return nvml.Return(nvmlDeviceCreateGpuInstance(device, C.uint(profile), &gi))
	}
	p := C.nvmlGpuInstancePlacement_t{start: C.uint(placement.Start), size: C.uint(placement.Size)}
	return nvml.Return(nvmlDeviceCreateGpuInstanceWithPlacement(device, C.uint(profile), &p, &gi))
}

func migComputeProfileForTest(handle unsafe.Pointer, version int) nvml.Return {
	gi := C.nvmlGpuInstance_t(handle)
	if version == 1 {
		var info C.nvmlComputeInstanceProfileInfo_t
		return nvml.Return(nvmlGpuInstanceGetComputeInstanceProfileInfo(gi, 0, 0, &info))
	}
	var v2 C.nvmlComputeInstanceProfileInfo_v2_t
	var v3 C.nvmlComputeInstanceProfileInfo_v3_t
	v2.version = C.uint(FabricStructVersion(C.sizeof_nvmlComputeInstanceProfileInfo_v2_t, 2))
	info := &v2
	if version == 3 {
		v3.version = C.uint(computeInstanceProfileInfoV3Version)
		info = (*C.nvmlComputeInstanceProfileInfo_v2_t)(unsafe.Pointer(&v3))
	}
	return nvml.Return(nvmlGpuInstanceGetComputeInstanceProfileInfoV(gi, 0, 0, info))
}
