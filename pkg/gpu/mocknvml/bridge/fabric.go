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

// Package main provides NVML GPU fabric (NVLink ComputeDomain) functions.
// This file contains hand-written implementations for:
//   - nvmlDeviceGetGpuFabricInfo (v1 struct, no version field)
//   - nvmlDeviceGetGpuFabricInfoV (versioned, dispatches on version field)
//
// These power ComputeDomain simulation on KIND clusters: the real
// dra-driver-nvidia-gpu controller and daemon read these APIs to learn
// the NVLink domain UUID and clique each GPU belongs to. See issue
// NVIDIA/k8s-test-infra#304 for the full design.

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

// nvmlGpuFabricInfo_v2 / _v3 version identifiers — match the upstream
// NVML_STRUCT_VERSION(GpuFabricInfo, N) encoding (size | (version << 24))
// computed at runtime so we are robust to struct padding differences.
func FabricStructVersion(size uintptr, version uint32) uint32 {
	return uint32(size) | (version << 24)
}

//export nvmlDeviceGetGpuFabricInfo
func nvmlDeviceGetGpuFabricInfo(device C.nvmlDevice_t, gpuFabricInfo *C.nvmlGpuFabricInfo_t) C.nvmlReturn_t {
	if gpuFabricInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev, ok := engine.GetEngine().LookupDevice(handle).(*engine.ConfigurableDevice)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetMockFabricInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	for i := 0; i < len(info.ClusterUUID); i++ {
		gpuFabricInfo.clusterUuid[i] = C.uchar(info.ClusterUUID[i])
	}
	gpuFabricInfo.status = C.nvmlReturn_t(info.Status)
	gpuFabricInfo.cliqueId = C.uint(info.CliqueID)
	gpuFabricInfo.state = C.nvmlGpuFabricState_t(info.State)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetGpuFabricInfoV
func nvmlDeviceGetGpuFabricInfoV(device C.nvmlDevice_t, gpuFabricInfo *C.nvmlGpuFabricInfoV_t) C.nvmlReturn_t {
	if gpuFabricInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := uintptr(unsafe.Pointer(device.handle))
	dev, ok := engine.GetEngine().LookupDevice(handle).(*engine.ConfigurableDevice)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetMockFabricInfoV()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	// The caller selects the struct version by writing into the
	// `version` field before calling. Only the v2 and v3 encodings
	// are valid; anything else — including a zero/unset version —
	// gets ARGUMENT_VERSION_MISMATCH, matching real NVML behaviour.
	// Accepting zero as v3 used to be a path that could write v3
	// bytes into a v2-sized buffer cast to *nvmlGpuFabricInfoV_t.
	requested := uint32(gpuFabricInfo.version)
	v2Tag := FabricStructVersion(unsafe.Sizeof(C.nvmlGpuFabricInfo_v2_t{}), 2)
	v3Tag := FabricStructVersion(unsafe.Sizeof(C.nvmlGpuFabricInfo_v3_t{}), 3)

	switch ClassifyFabricVersion(requested, v2Tag, v3Tag) {
	case FabricVersionV2:
		// Reinterpret as v2 layout. The two structs share their
		// leading fields up through healthMask; v3 just adds
		// healthSummary at the tail, which v2 callers must not touch.
		v2info := (*C.nvmlGpuFabricInfo_v2_t)(unsafe.Pointer(gpuFabricInfo))
		v2info.version = C.uint(v2Tag)
		for i := 0; i < len(info.ClusterUUID); i++ {
			v2info.clusterUuid[i] = C.uchar(info.ClusterUUID[i])
		}
		v2info.status = C.nvmlReturn_t(info.Status)
		v2info.cliqueId = C.uint(info.CliqueID)
		v2info.state = C.nvmlGpuFabricState_t(info.State)
		v2info.healthMask = C.uint(info.HealthMask)
		return C.NVML_SUCCESS
	case FabricVersionV3:
		gpuFabricInfo.version = C.uint(v3Tag)
		for i := 0; i < len(info.ClusterUUID); i++ {
			gpuFabricInfo.clusterUuid[i] = C.uchar(info.ClusterUUID[i])
		}
		gpuFabricInfo.status = C.nvmlReturn_t(info.Status)
		gpuFabricInfo.cliqueId = C.uint(info.CliqueID)
		gpuFabricInfo.state = C.nvmlGpuFabricState_t(info.State)
		gpuFabricInfo.healthMask = C.uint(info.HealthMask)
		gpuFabricInfo.healthSummary = C.uchar(info.HealthSummary)
		return C.NVML_SUCCESS
	default:
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
}
