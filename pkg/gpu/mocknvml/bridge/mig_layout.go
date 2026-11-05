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

// Sizes and offsets of the MIG C structs, exposed so a pure-Go test can pin
// them against go-nvml's Go structs. Callers allocate these buffers from those
// Go types, so a mismatch means the bridge writes past the caller's
// allocation — which the header's comments promise it does not.

package main

/*
#include "nvml_types.h"
*/
import "C"
import "unsafe"

func gpuInstancePlacementSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpuInstancePlacement_t{})
}

func gpuInstanceProfileInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpuInstanceProfileInfo_t{})
}

func gpuInstanceProfileInfoV2SizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpuInstanceProfileInfo_v2_t{})
}

func gpuInstanceProfileInfoV3SizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpuInstanceProfileInfo_v3_t{})
}

func gpuInstanceProfileInfoMemorySizeOffsetForTest() uintptr {
	var info C.nvmlGpuInstanceProfileInfo_t
	return unsafe.Offsetof(info.memorySizeMB)
}

func gpuInstanceProfileInfoV2NameOffsetForTest() uintptr {
	var info C.nvmlGpuInstanceProfileInfo_v2_t
	return unsafe.Offsetof(info.name)
}

func gpuInstanceProfileInfoV3CapabilitiesOffsetForTest() uintptr {
	var info C.nvmlGpuInstanceProfileInfo_v3_t
	return unsafe.Offsetof(info.capabilities)
}

func gpuInstanceInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlGpuInstanceInfo_t{})
}

func gpuInstanceInfoPlacementOffsetForTest() uintptr {
	var info C.nvmlGpuInstanceInfo_t
	return unsafe.Offsetof(info.placement)
}

func computeInstancePlacementSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlComputeInstancePlacement_t{})
}

func computeInstanceProfileInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlComputeInstanceProfileInfo_t{})
}

func computeInstanceProfileInfoV2SizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlComputeInstanceProfileInfo_v2_t{})
}

func computeInstanceProfileInfoV3SizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlComputeInstanceProfileInfo_v3_t{})
}

func computeInstanceProfileInfoV2NameOffsetForTest() uintptr {
	var info C.nvmlComputeInstanceProfileInfo_v2_t
	return unsafe.Offsetof(info.name)
}

func computeInstanceInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlComputeInstanceInfo_t{})
}

func computeInstanceInfoPlacementOffsetForTest() uintptr {
	var info C.nvmlComputeInstanceInfo_t
	return unsafe.Offsetof(info.placement)
}

func deviceAttributesSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlDeviceAttributes_t{})
}

func deviceAttributesMemorySizeOffsetForTest() uintptr {
	var attrs C.nvmlDeviceAttributes_t
	return unsafe.Offsetof(attrs.memorySizeMB)
}

func deviceNameV2BufferSizeForTest() int {
	return C.NVML_DEVICE_NAME_V2_BUFFER_SIZE
}
