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
	"testing"
)

func newTestEngine() *Engine {
	return NewEngine()
}

func TestInit(t *testing.T) {
	e := newTestEngine()
	if err := e.Init(0); err != CudaSuccess {
		t.Fatalf("Init(0) = %d, want %d", err, CudaSuccess)
	}
}

func TestDriverGetVersion(t *testing.T) {
	e := newTestEngine()
	ver, err := e.DriverGetVersion()
	if err != CudaSuccess {
		t.Fatalf("DriverGetVersion() error = %d", err)
	}
	if ver != DefaultDriverVersion {
		t.Fatalf("DriverGetVersion() = %d, want %d", ver, DefaultDriverVersion)
	}
}

func TestGetDeviceCount(t *testing.T) {
	e := newTestEngine()
	count, err := e.GetDeviceCount()
	if err != CudaSuccess {
		t.Fatalf("GetDeviceCount() error = %d", err)
	}
	if count != DefaultDeviceCount {
		t.Fatalf("GetDeviceCount() = %d, want %d", count, DefaultDeviceCount)
	}
}

func TestSetDevice(t *testing.T) {
	e := newTestEngine()
	if err := e.SetDevice(0); err != CudaSuccess {
		t.Fatalf("SetDevice(0) = %d, want SUCCESS", err)
	}
	if err := e.SetDevice(7); err != CudaSuccess {
		t.Fatalf("SetDevice(7) = %d, want SUCCESS", err)
	}
	if err := e.SetDevice(8); err != CudaErrorInvalidDevice {
		t.Fatalf("SetDevice(8) = %d, want CudaErrorInvalidDevice", err)
	}
	if err := e.SetDevice(-1); err != CudaErrorInvalidDevice {
		t.Fatalf("SetDevice(-1) = %d, want CudaErrorInvalidDevice", err)
	}
}

func TestMallocFreeLifecycle(t *testing.T) {
	e := newTestEngine()

	// Allocate
	ptr, err := e.Malloc(1024)
	if err != CudaSuccess {
		t.Fatalf("Malloc(1024) error = %d", err)
	}
	if ptr == 0 {
		t.Fatal("Malloc returned nil pointer")
	}
	if e.AllocationCount() != 1 {
		t.Fatalf("AllocationCount = %d, want 1", e.AllocationCount())
	}

	// Verify allocation info
	info, ok := e.GetAllocation(ptr)
	if !ok {
		t.Fatal("GetAllocation returned false for allocated pointer")
	}
	if info.Size != 1024 {
		t.Fatalf("AllocationInfo.Size = %d, want 1024", info.Size)
	}

	// Free
	if err := e.Free(ptr); err != CudaSuccess {
		t.Fatalf("Free() = %d, want SUCCESS", err)
	}
	if e.AllocationCount() != 0 {
		t.Fatalf("AllocationCount after free = %d, want 0", e.AllocationCount())
	}

	// Double free should fail
	if err := e.Free(ptr); err != CudaErrorInvalidValue {
		t.Fatalf("Double Free() = %d, want CudaErrorInvalidValue", err)
	}

	// Free(NULL) is no-op
	if err := e.Free(0); err != CudaSuccess {
		t.Fatalf("Free(0) = %d, want SUCCESS", err)
	}
}

func TestMallocZeroSize(t *testing.T) {
	e := newTestEngine()
	_, err := e.Malloc(0)
	if err != CudaErrorInvalidValue {
		t.Fatalf("Malloc(0) = %d, want CudaErrorInvalidValue", err)
	}
}

func TestMultipleAllocations(t *testing.T) {
	e := newTestEngine()
	ptrs := make([]uintptr, 5)
	for i := range ptrs {
		ptr, err := e.Malloc(uint64(1024 * (i + 1)))
		if err != CudaSuccess {
			t.Fatalf("Malloc(%d) error = %d", 1024*(i+1), err)
		}
		ptrs[i] = ptr
	}
	if e.AllocationCount() != 5 {
		t.Fatalf("AllocationCount = %d, want 5", e.AllocationCount())
	}

	// All pointers should be unique
	seen := make(map[uintptr]bool)
	for _, ptr := range ptrs {
		if seen[ptr] {
			t.Fatal("Duplicate pointer returned from Malloc")
		}
		seen[ptr] = true
	}

	// Free all
	for _, ptr := range ptrs {
		if err := e.Free(ptr); err != CudaSuccess {
			t.Fatalf("Free(0x%x) = %d", ptr, err)
		}
	}
	if e.AllocationCount() != 0 {
		t.Fatalf("AllocationCount after freeing all = %d, want 0", e.AllocationCount())
	}
}

func TestMemcpy(t *testing.T) {
	e := newTestEngine()
	if err := e.Memcpy(CudaMemcpyHostToHost); err != CudaSuccess {
		t.Fatalf("Memcpy(HostToHost) = %d", err)
	}
	if err := e.Memcpy(CudaMemcpyHostToDevice); err != CudaSuccess {
		t.Fatalf("Memcpy(HostToDevice) = %d", err)
	}
	if err := e.Memcpy(CudaMemcpyDeviceToHost); err != CudaSuccess {
		t.Fatalf("Memcpy(DeviceToHost) = %d", err)
	}
}

func TestLaunchKernel(t *testing.T) {
	e := newTestEngine()
	if err := e.LaunchKernel(); err != CudaSuccess {
		t.Fatalf("LaunchKernel() = %d", err)
	}
}

func TestDeviceSynchronize(t *testing.T) {
	e := newTestEngine()
	if err := e.DeviceSynchronize(); err != CudaSuccess {
		t.Fatalf("DeviceSynchronize() = %d", err)
	}
}

func TestGetErrorString(t *testing.T) {
	e := newTestEngine()
	tests := []struct {
		err  CudaError
		want string
	}{
		{CudaSuccess, "no error"},
		{CudaErrorInvalidValue, "invalid argument"},
		{CudaErrorMemoryAllocation, "out of memory"},
		{CudaErrorInvalidDevice, "invalid device ordinal"},
		{CudaError(12345), "unknown error"},
	}
	for _, tc := range tests {
		got := e.GetErrorString(tc.err)
		if got != tc.want {
			t.Errorf("GetErrorString(%d) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestSetDeviceTracksCurrent(t *testing.T) {
	e := newTestEngine()
	e.SetDevice(3)
	ptr, _ := e.Malloc(512)
	info, ok := e.GetAllocation(ptr)
	if !ok {
		t.Fatal("allocation not found")
	}
	if info.DeviceID != 3 {
		t.Fatalf("allocation device = %d, want 3", info.DeviceID)
	}
}

func TestErrorStringFunction(t *testing.T) {
	if s := ErrorString(CudaSuccess); s != "no error" {
		t.Fatalf("ErrorString(0) = %q, want 'no error'", s)
	}
	if s := ErrorString(CudaError(99999)); s != "unknown error" {
		t.Fatalf("ErrorString(99999) = %q, want 'unknown error'", s)
	}
}
