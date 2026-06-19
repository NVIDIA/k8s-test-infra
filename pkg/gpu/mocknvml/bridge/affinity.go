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

// Package main provides NVML CPU / memory affinity bridge functions. The
// affinity bitmasks are computed in the pure-Go engine from each device's
// NUMA node in the NodeFabric; this file only marshals the []uint words
// into the caller's C.ulong array.

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

// writeAffinityMask copies a []uint affinity bitmask into the caller's
// C.ulong set of setSize words. Returns INSUFFICIENT_SIZE when the buffer
// is too small, matching real NVML.
func writeAffinityMask(set *C.ulong, setSize C.uint, mask []uint) C.nvmlReturn_t {
	if set == nil || setSize == 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if int(setSize) < len(mask) {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	out := unsafe.Slice(set, int(setSize))
	for i := range out {
		out[i] = 0
	}
	for i, w := range mask {
		out[i] = C.ulong(w)
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetCpuAffinity
func nvmlDeviceGetCpuAffinity(device C.nvmlDevice_t, cpuSetSize C.uint, cpuSet *C.ulong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCpuAffinity"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mask, ret := dev.GetCpuAffinity(int(cpuSetSize))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return writeAffinityMask(cpuSet, cpuSetSize, mask)
}

//export nvmlDeviceGetCpuAffinityWithinScope
func nvmlDeviceGetCpuAffinityWithinScope(device C.nvmlDevice_t, cpuSetSize C.uint, cpuSet *C.ulong, scope C.nvmlAffinityScope_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetCpuAffinityWithinScope"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mask, ret := dev.GetCpuAffinityWithinScope(int(cpuSetSize), nvml.AffinityScope(scope))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return writeAffinityMask(cpuSet, cpuSetSize, mask)
}

//export nvmlDeviceGetMemoryAffinity
func nvmlDeviceGetMemoryAffinity(device C.nvmlDevice_t, nodeSetSize C.uint, nodeSet *C.ulong, scope C.nvmlAffinityScope_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetMemoryAffinity"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mask, ret := dev.GetMemoryAffinity(int(nodeSetSize), nvml.AffinityScope(scope))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return writeAffinityMask(nodeSet, nodeSetSize, mask)
}

//export nvmlDeviceGetNumaNodeId
func nvmlDeviceGetNumaNodeId(device C.nvmlDevice_t, node *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNumaNodeId"); !ok {
		return ret
	}
	if node == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	id, ret := dev.GetNumaNodeId()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*node = C.uint(id)
	return C.NVML_SUCCESS
}
