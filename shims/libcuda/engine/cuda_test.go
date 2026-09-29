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

	"github.com/stretchr/testify/require"
)

func newTestEngine() *Engine {
	return NewEngine()
}

func TestInit(t *testing.T) {
	e := newTestEngine()
	require.Equal(t, CudaSuccess, e.Init(0), "Init(0)")
}

func TestDriverGetVersion(t *testing.T) {
	e := newTestEngine()
	ver, err := e.DriverGetVersion()
	require.Equal(t, CudaSuccess, err, "DriverGetVersion() error")
	require.Equal(t, DefaultDriverVersion, ver, "DriverGetVersion()")
}

func TestGetDeviceCount(t *testing.T) {
	e := newTestEngine()
	count, err := e.GetDeviceCount()
	require.Equal(t, CudaSuccess, err, "GetDeviceCount() error")
	require.Equal(t, DefaultDeviceCount, count, "GetDeviceCount()")
}

func TestSetDevice(t *testing.T) {
	e := newTestEngine()
	require.Equal(t, CudaSuccess, e.SetDevice(0), "SetDevice(0)")
	require.Equal(t, CudaSuccess, e.SetDevice(7), "SetDevice(7)")
	require.Equal(t, CudaErrorInvalidDevice, e.SetDevice(8), "SetDevice(8)")
	require.Equal(t, CudaErrorInvalidDevice, e.SetDevice(-1), "SetDevice(-1)")
}

func TestMallocFreeLifecycle(t *testing.T) {
	e := newTestEngine()

	// Allocate
	ptr, err := e.Malloc(1024)
	require.Equal(t, CudaSuccess, err, "Malloc(1024) error")
	require.NotZero(t, ptr, "Malloc returned nil pointer")
	require.Equal(t, 1, e.AllocationCount(), "AllocationCount")

	// Verify allocation info
	info, ok := e.GetAllocation(ptr)
	require.True(t, ok, "GetAllocation returned false for allocated pointer")
	require.Equal(t, uint64(1024), info.Size, "AllocationInfo.Size")

	// Free
	require.Equal(t, CudaSuccess, e.Free(ptr), "Free()")
	require.Equal(t, 0, e.AllocationCount(), "AllocationCount after free")

	// Double free should fail
	require.Equal(t, CudaErrorInvalidValue, e.Free(ptr), "Double Free()")

	// Free(NULL) is no-op
	require.Equal(t, CudaSuccess, e.Free(0), "Free(0)")
}

func TestMallocZeroSize(t *testing.T) {
	e := newTestEngine()
	_, err := e.Malloc(0)
	require.Equal(t, CudaErrorInvalidValue, err, "Malloc(0)")
}

func TestMultipleAllocations(t *testing.T) {
	e := newTestEngine()
	ptrs := make([]uintptr, 5)
	for i := range ptrs {
		ptr, err := e.Malloc(uint64(1024 * (i + 1)))
		require.Equal(t, CudaSuccess, err, "Malloc(%d) error", 1024*(i+1))
		ptrs[i] = ptr
	}
	require.Equal(t, 5, e.AllocationCount(), "AllocationCount")

	// All pointers should be unique
	seen := make(map[uintptr]bool)
	for _, ptr := range ptrs {
		require.False(t, seen[ptr], "Duplicate pointer returned from Malloc")
		seen[ptr] = true
	}

	// Free all
	for _, ptr := range ptrs {
		require.Equal(t, CudaSuccess, e.Free(ptr), "Free(0x%x)", ptr)
	}
	require.Equal(t, 0, e.AllocationCount(), "AllocationCount after freeing all")
}

func TestMemcpy(t *testing.T) {
	e := newTestEngine()
	require.Equal(t, CudaSuccess, e.Memcpy(CudaMemcpyHostToHost), "Memcpy(HostToHost)")
	require.Equal(t, CudaSuccess, e.Memcpy(CudaMemcpyHostToDevice), "Memcpy(HostToDevice)")
	require.Equal(t, CudaSuccess, e.Memcpy(CudaMemcpyDeviceToHost), "Memcpy(DeviceToHost)")
}

func TestLaunchKernel(t *testing.T) {
	e := newTestEngine()
	require.Equal(t, CudaSuccess, e.LaunchKernel(), "LaunchKernel()")
}

func TestDeviceSynchronize(t *testing.T) {
	e := newTestEngine()
	require.Equal(t, CudaSuccess, e.DeviceSynchronize(), "DeviceSynchronize()")
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
		require.Equal(t, tc.want, got, "GetErrorString(%d)", tc.err)
	}
}

func TestSetDeviceTracksCurrent(t *testing.T) {
	e := newTestEngine()
	e.SetDevice(3)
	ptr, _ := e.Malloc(512)
	info, ok := e.GetAllocation(ptr)
	require.True(t, ok, "allocation not found")
	require.Equal(t, 3, info.DeviceID, "allocation device")
}

func TestErrorStringFunction(t *testing.T) {
	require.Equal(t, "no error", ErrorString(CudaSuccess), "ErrorString(0)")
	require.Equal(t, "unknown error", ErrorString(CudaError(99999)), "ErrorString(99999)")
}
