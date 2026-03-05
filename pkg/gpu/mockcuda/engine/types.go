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

package engine

// CudaError represents CUDA runtime API error codes.
type CudaError int

const (
	CudaSuccess                     CudaError = 0
	CudaErrorInvalidValue           CudaError = 1
	CudaErrorMemoryAllocation       CudaError = 2
	CudaErrorInitializationError    CudaError = 3
	CudaErrorInvalidDevice          CudaError = 10
	CudaErrorInvalidMemcpyDirection CudaError = 21
	CudaErrorNotReady               CudaError = 34
	CudaErrorUnknown                CudaError = 999
)

// CudaMemcpyKind specifies the direction of a memory copy.
type CudaMemcpyKind int

const (
	CudaMemcpyHostToHost     CudaMemcpyKind = 0
	CudaMemcpyHostToDevice   CudaMemcpyKind = 1
	CudaMemcpyDeviceToHost   CudaMemcpyKind = 2
	CudaMemcpyDeviceToDevice CudaMemcpyKind = 3
	CudaMemcpyDefault        CudaMemcpyKind = 4
)

// AllocationInfo tracks a single GPU memory allocation.
type AllocationInfo struct {
	Ptr      uintptr
	Size     uint64
	DeviceID int
}

// errorStrings maps CUDA error codes to human-readable strings.
var errorStrings = map[CudaError]string{
	CudaSuccess:                     "no error",
	CudaErrorInvalidValue:           "invalid argument",
	CudaErrorMemoryAllocation:       "out of memory",
	CudaErrorInitializationError:    "initialization error",
	CudaErrorInvalidDevice:          "invalid device ordinal",
	CudaErrorInvalidMemcpyDirection: "invalid memcpy direction",
	CudaErrorNotReady:               "device not ready",
	CudaErrorUnknown:                "unknown error",
}

// ErrorString returns the human-readable string for a CUDA error code.
func ErrorString(err CudaError) string {
	if s, ok := errorStrings[err]; ok {
		return s
	}
	return "unknown error"
}
