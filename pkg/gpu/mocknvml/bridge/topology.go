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

// Package main provides NVML topology bridge functions. This file carries
// the hand-written nearest-GPU lookup, derived from the NodeFabric's
// pairwise PCIe topology levels. The pairwise common-ancestor export lives
// in device.go; both are thin marshalling layers over the pure-Go engine.
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

//export nvmlDeviceGetTopologyNearestGpus
func nvmlDeviceGetTopologyNearestGpus(device C.nvmlDevice_t, level C.nvmlGpuTopologyLevel_t, count *C.uint, deviceArray *C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetTopologyNearestGpus"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := unsafe.Pointer(device.handle)
	peers, ret := engine.GetEngine().TopologyNearestGpus(handle, nvml.GpuTopologyLevel(level))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	// A zero count is the size query NVML documents for this family, whatever
	// the array pointer holds: a caller with a fixed-size stack array asks "how
	// many?" by zeroing the count, not by giving up its buffer. The NULL-array
	// probe is undocumented but a harmless superset, and go-nvml uses it.
	if deviceArray == nil || *count == 0 {
		*count = C.uint(len(peers))
		return C.NVML_SUCCESS
	}

	// INSUFFICIENT_SIZE is not a documented return here, unlike the array
	// getters elsewhere in the bridge: the fill call writes the count the
	// caller asked for and reports how many that turned out to be.
	written := min(int(*count), len(peers))
	out := unsafe.Slice(deviceArray, written)
	for i, h := range peers[:written] {
		out[i].handle = (*C.struct_nvmlDevice_st)(h)
	}
	*count = C.uint(written)
	return C.NVML_SUCCESS
}

//export nvmlSystemGetTopologyGpuSet
func nvmlSystemGetTopologyGpuSet(cpuNumber C.uint, count *C.uint, deviceArray *C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemGetTopologyGpuSet"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	gpus, ret := engine.GetEngine().TopologyGpuSet(int(cpuNumber))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	// Same size-query and bounded-fill contract as the nearest-GPU query above,
	// word for word in the header.
	if deviceArray == nil || *count == 0 {
		*count = C.uint(len(gpus))
		return C.NVML_SUCCESS
	}

	written := min(int(*count), len(gpus))
	out := unsafe.Slice(deviceArray, written)
	for i, h := range gpus[:written] {
		out[i].handle = (*C.struct_nvmlDevice_st)(h)
	}
	*count = C.uint(written)
	return C.NVML_SUCCESS
}
