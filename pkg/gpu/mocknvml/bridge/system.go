// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

// Package main provides NVML system-level functions.
// This file contains the hand-written implementations for:
// - nvmlSystemGetDriverVersion
// - nvmlSystemGetNVMLVersion
// - nvmlSystemGetCudaDriverVersion, nvmlSystemGetCudaDriverVersion_v2

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlSystemGetDriverVersion
func nvmlSystemGetDriverVersion(version unsafe.Pointer, length C.uint) C.nvmlReturn_t {
	if version == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	driverVersion, ret := engine.GetEngine().SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(driverVersion, (*C.char)(version), length)
}

//export nvmlSystemGetNVMLVersion
func nvmlSystemGetNVMLVersion(version unsafe.Pointer, length C.uint) C.nvmlReturn_t {
	if version == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	nvmlVersion, ret := engine.GetEngine().SystemGetNVMLVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	return goStringToC(nvmlVersion, (*C.char)(version), length)
}

//export nvmlSystemGetProcessName
func nvmlSystemGetProcessName(pid C.uint, name *C.char, length C.uint) C.nvmlReturn_t {
	if name == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// nvidia-smi resolves the process name for each configured PID through this
	// call (its internal process list carries only pid + memory). Unknown pids
	// keep the previous stub behaviour rather than answering NOT_SUPPORTED
	// outright, so version gating (FUNCTION_NOT_FOUND on drivers without this
	// symbol) and strict mode still apply to lookups we cannot satisfy.
	procName := engine.GetEngine().ProcessNameByPID(uint32(pid))
	if procName == "" {
		return stubReturn("nvmlSystemGetProcessName")
	}
	return goStringToC(procName, name, length)
}

//export nvmlSystemGetCudaDriverVersion
func nvmlSystemGetCudaDriverVersion(cudaDriverVersion unsafe.Pointer) C.nvmlReturn_t {
	return nvmlSystemGetCudaDriverVersion_v2(cudaDriverVersion)
}

//export nvmlSystemGetCudaDriverVersion_v2
func nvmlSystemGetCudaDriverVersion_v2(cudaDriverVersion unsafe.Pointer) C.nvmlReturn_t {
	if cudaDriverVersion == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	ver, ret := engine.GetEngine().SystemGetCudaDriverVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*(*C.int)(cudaDriverVersion) = C.int(ver)
	return C.NVML_SUCCESS
}
