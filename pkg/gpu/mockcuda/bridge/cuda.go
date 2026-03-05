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

// Package main provides CUDA Runtime/Driver API bridge functions.
// Built as a c-shared library to produce libcuda.so.1.

package main

/*
#include <stdlib.h>
#include <string.h>
#include "cuda_types.h"
*/
import "C"
import (
	"unsafe"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockcuda/engine"
)

// =============================================================================
// Initialization
// =============================================================================

//export cuInit
func cuInit(flags C.uint) C.CUresult {
	return C.CUresult(toCudaError(engine.GetEngine().Init(uint(flags))))
}

//export cudaDriverGetVersion
func cudaDriverGetVersion(driverVersion *C.int) C.cudaError_t {
	if driverVersion == nil {
		return C.cudaErrorInvalidValue
	}
	ver, err := engine.GetEngine().DriverGetVersion()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*driverVersion = C.int(ver)
	return C.cudaSuccess
}

// =============================================================================
// Device Management
// =============================================================================

//export cudaGetDeviceCount
func cudaGetDeviceCount(count *C.int) C.cudaError_t {
	if count == nil {
		return C.cudaErrorInvalidValue
	}
	c, err := engine.GetEngine().GetDeviceCount()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*count = C.int(c)
	return C.cudaSuccess
}

//export cudaSetDevice
func cudaSetDevice(device C.int) C.cudaError_t {
	return toCudaError(engine.GetEngine().SetDevice(int(device)))
}

// =============================================================================
// Memory Management
// =============================================================================

//export cudaMalloc
func cudaMalloc(devPtr *unsafe.Pointer, size C.size_t) C.cudaError_t {
	if devPtr == nil {
		return C.cudaErrorInvalidValue
	}
	// Allocate real C memory so the pointer is valid and go vet clean.
	// The engine tracks it by uintptr key for bookkeeping.
	cPtr := C.malloc(C.size_t(size))
	if cPtr == nil {
		return C.cudaErrorMemoryAllocation
	}
	err := engine.GetEngine().TrackAllocation(uintptr(cPtr), uint64(size))
	if err != engine.CudaSuccess {
		C.free(cPtr)
		return toCudaError(err)
	}
	*devPtr = cPtr
	return C.cudaSuccess
}

//export cudaFree
func cudaFree(devPtr unsafe.Pointer) C.cudaError_t {
	err := engine.GetEngine().Free(uintptr(devPtr))
	if err == engine.CudaSuccess && devPtr != nil {
		C.free(devPtr)
	}
	return toCudaError(err)
}

//export cudaMemcpy
func cudaMemcpy(dst unsafe.Pointer, src unsafe.Pointer, count C.size_t, kind C.cudaMemcpyKind) C.cudaError_t {
	if count == 0 {
		return C.cudaSuccess
	}
	// For host-to-host, do a real copy
	if kind == C.cudaMemcpyHostToHost {
		if dst == nil || src == nil {
			return C.cudaErrorInvalidValue
		}
		C.memcpy(dst, src, count)
	}
	return toCudaError(engine.GetEngine().Memcpy(engine.CudaMemcpyKind(kind)))
}

// =============================================================================
// Execution
// =============================================================================

//export cudaLaunchKernel
func cudaLaunchKernel(
	funcPtr unsafe.Pointer,
	gridDim C.dim3,
	blockDim C.dim3,
	args *unsafe.Pointer,
	sharedMem C.size_t,
	stream C.cudaStream_t,
) C.cudaError_t {
	return toCudaError(engine.GetEngine().LaunchKernel())
}

//export cudaDeviceSynchronize
func cudaDeviceSynchronize() C.cudaError_t {
	return toCudaError(engine.GetEngine().DeviceSynchronize())
}

// =============================================================================
// Error Handling
// =============================================================================

//export cudaGetErrorString
func cudaGetErrorString(err C.cudaError_t) *C.char {
	return errStrings.get(engine.CudaError(err))
}

//export cudaGetLastError
func cudaGetLastError() C.cudaError_t {
	return C.cudaSuccess
}

//export cudaPeekAtLastError
func cudaPeekAtLastError() C.cudaError_t {
	return C.cudaSuccess
}

// =============================================================================
// Additional stubs commonly needed by GPU Operator Validator
// =============================================================================

//export cudaGetDevice
func cudaGetDevice(device *C.int) C.cudaError_t {
	if device == nil {
		return C.cudaErrorInvalidValue
	}
	d, err := engine.GetEngine().GetDevice()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*device = C.int(d)
	return C.cudaSuccess
}

//export cudaDeviceReset
func cudaDeviceReset() C.cudaError_t {
	return C.cudaSuccess
}

//export cudaRuntimeGetVersion
func cudaRuntimeGetVersion(runtimeVersion *C.int) C.cudaError_t {
	if runtimeVersion == nil {
		return C.cudaErrorInvalidValue
	}
	// Return same as driver version for simplicity
	ver, err := engine.GetEngine().DriverGetVersion()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*runtimeVersion = C.int(ver)
	return C.cudaSuccess
}
