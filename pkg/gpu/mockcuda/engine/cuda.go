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

import (
	"fmt"
	"os"
	"strconv"
	"sync"
)

// DefaultDeviceCount is the number of devices when no config is provided.
const DefaultDeviceCount = 8

// DefaultDriverVersion is the CUDA driver version reported by default.
const DefaultDriverVersion = 12080

var debugEnabled = os.Getenv("MOCK_CUDA_DEBUG") != ""

func debugLog(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

// Engine manages CUDA mock state: device count, current device, and allocations.
type Engine struct {
	mu            sync.Mutex
	initialized   bool
	deviceCount   int
	driverVersion int
	currentDevice int
	allocations   map[uintptr]AllocationInfo
	nextAllocID   uintptr
}

var (
	engineInstance *Engine
	engineOnce     sync.Once
)

// GetEngine returns the singleton CUDA Engine instance.
func GetEngine() *Engine {
	engineOnce.Do(func() {
		engineInstance = NewEngine()
	})
	return engineInstance
}

// ResetEngine resets the singleton for testing.
func ResetEngine() {
	engineOnce = sync.Once{}
	engineInstance = nil
}

// NewEngine creates a new CUDA engine with default or environment-configured state.
func NewEngine() *Engine {
	deviceCount := DefaultDeviceCount
	// Fallback: shared NVML config for device count
	if num := os.Getenv("MOCK_NVML_NUM_DEVICES"); num != "" {
		if val, err := strconv.Atoi(num); err == nil && val >= 0 {
			deviceCount = val
		}
	}
	// Override: CUDA-specific env var takes priority
	if num := os.Getenv("MOCK_CUDA_NUM_DEVICES"); num != "" {
		if val, err := strconv.Atoi(num); err == nil && val >= 0 {
			deviceCount = val
		}
	}

	driverVersion := DefaultDriverVersion
	if ver := os.Getenv("MOCK_CUDA_DRIVER_VERSION"); ver != "" {
		if val, err := strconv.Atoi(ver); err == nil && val > 0 {
			driverVersion = val
		}
	}

	return &Engine{
		deviceCount:   deviceCount,
		driverVersion: driverVersion,
		allocations:   make(map[uintptr]AllocationInfo),
		nextAllocID:   0x1000, // Start at a non-zero fake address
	}
}

// Init initializes the CUDA driver.
func (e *Engine) Init(flags uint) CudaError {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initialized = true
	debugLog("[CUDA] cuInit(%d) -> SUCCESS\n", flags)
	return CudaSuccess
}

// DriverGetVersion returns the CUDA driver version.
func (e *Engine) DriverGetVersion() (int, CudaError) {
	debugLog("[CUDA] cudaDriverGetVersion -> %d\n", e.driverVersion)
	return e.driverVersion, CudaSuccess
}

// GetDeviceCount returns the number of CUDA devices.
func (e *Engine) GetDeviceCount() (int, CudaError) {
	debugLog("[CUDA] cudaGetDeviceCount -> %d\n", e.deviceCount)
	return e.deviceCount, CudaSuccess
}

// SetDevice sets the current device.
func (e *Engine) SetDevice(deviceID int) CudaError {
	e.mu.Lock()
	defer e.mu.Unlock()
	if deviceID < 0 || deviceID >= e.deviceCount {
		debugLog("[CUDA] cudaSetDevice(%d) -> INVALID_DEVICE\n", deviceID)
		return CudaErrorInvalidDevice
	}
	e.currentDevice = deviceID
	debugLog("[CUDA] cudaSetDevice(%d) -> SUCCESS\n", deviceID)
	return CudaSuccess
}

// GetDevice returns the current device.
func (e *Engine) GetDevice() (int, CudaError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentDevice, CudaSuccess
}

// Malloc allocates tracked memory and returns a fake device pointer.
func (e *Engine) Malloc(size uint64) (uintptr, CudaError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if size == 0 {
		return 0, CudaErrorInvalidValue
	}
	ptr := e.nextAllocID
	e.nextAllocID += uintptr(size)
	// Align next allocation
	if e.nextAllocID%256 != 0 {
		e.nextAllocID += 256 - (e.nextAllocID % 256)
	}
	e.allocations[ptr] = AllocationInfo{
		Ptr:      ptr,
		Size:     size,
		DeviceID: e.currentDevice,
	}
	debugLog("[CUDA] cudaMalloc(%d) -> 0x%x\n", size, ptr)
	return ptr, CudaSuccess
}

// Free releases a tracked allocation.
func (e *Engine) Free(ptr uintptr) CudaError {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ptr == 0 {
		// cudaFree(NULL) is a no-op per CUDA spec
		return CudaSuccess
	}
	if _, ok := e.allocations[ptr]; !ok {
		debugLog("[CUDA] cudaFree(0x%x) -> INVALID_VALUE (not tracked)\n", ptr)
		return CudaErrorInvalidValue
	}
	delete(e.allocations, ptr)
	debugLog("[CUDA] cudaFree(0x%x) -> SUCCESS\n", ptr)
	return CudaSuccess
}

// Memcpy performs a memory copy operation. For host-to-host it does real copies
// via the bridge layer. For device copies it's a tracked no-op.
func (e *Engine) Memcpy(kind CudaMemcpyKind) CudaError {
	debugLog("[CUDA] cudaMemcpy(kind=%d) -> SUCCESS\n", kind)
	return CudaSuccess
}

// LaunchKernel is a no-op stub for kernel launches.
func (e *Engine) LaunchKernel() CudaError {
	debugLog("[CUDA] cudaLaunchKernel -> SUCCESS\n")
	return CudaSuccess
}

// DeviceSynchronize is a no-op.
func (e *Engine) DeviceSynchronize() CudaError {
	debugLog("[CUDA] cudaDeviceSynchronize -> SUCCESS\n")
	return CudaSuccess
}

// GetErrorString returns the error string for a CUDA error code.
func (e *Engine) GetErrorString(err CudaError) string {
	return ErrorString(err)
}

// AllocationCount returns the number of active allocations (for testing).
func (e *Engine) AllocationCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.allocations)
}

// GetAllocation returns the allocation info for a pointer (for testing).
func (e *Engine) GetAllocation(ptr uintptr) (AllocationInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	info, ok := e.allocations[ptr]
	return info, ok
}
