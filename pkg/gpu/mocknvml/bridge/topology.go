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
	handle := uintptr(unsafe.Pointer(device.handle))
	peers, ret := engine.GetEngine().TopologyNearestGpus(handle, nvml.GpuTopologyLevel(level))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	// First call (deviceArray nil) returns the required size, matching the
	// two-phase NVML query convention.
	if deviceArray == nil {
		*count = C.uint(len(peers))
		return C.NVML_SUCCESS
	}
	if int(*count) < len(peers) {
		*count = C.uint(len(peers))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(deviceArray, len(peers))
	for i, h := range peers {
		//nolint:govet // uintptr->unsafe.Pointer: handle is C memory from HandleTable.Register
		out[i].handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(h))
	}
	*count = C.uint(len(peers))
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

	// Two-phase NVML query convention: a nil array returns the size.
	if deviceArray == nil {
		*count = C.uint(len(gpus))
		return C.NVML_SUCCESS
	}
	if int(*count) < len(gpus) {
		*count = C.uint(len(gpus))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(deviceArray, len(gpus))
	for i, h := range gpus {
		//nolint:govet // uintptr->unsafe.Pointer: handle is C memory from HandleTable.Register
		out[i].handle = (*C.struct_nvmlDevice_st)(unsafe.Pointer(h))
	}
	*count = C.uint(len(gpus))
	return C.NVML_SUCCESS
}
