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

package engine

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	"github.com/stretchr/testify/require"
)

func TestHandleTable_NewHandleTable(t *testing.T) {
	ht := NewHandleTable()
	require.NotNil(t, ht, "NewHandleTable returned nil")
	require.Equal(t, 0, ht.Count(), "Expected empty table")
}

func TestHandleTable_Register(t *testing.T) {
	ht := NewHandleTable()
	dev := dgxa100.NewDevice(0)

	// Register device
	handle := ht.Register(dev)
	require.NotZero(t, handle, "Expected non-zero handle")
	require.Equal(t, 1, ht.Count(), "Expected count 1")

	// Register same device again - should return same handle
	handle2 := ht.Register(dev)
	require.Equal(t, handle, handle2, "Expected same handle for same device")
	require.Equal(t, 1, ht.Count(), "Expected count to remain 1")
}

func TestHandleTable_RegisterNil(t *testing.T) {
	ht := NewHandleTable()
	handle := ht.Register(nil)
	require.Zero(t, handle, "Expected 0 handle for nil device")
	require.Equal(t, 0, ht.Count(), "Expected count 0")
}

func TestHandleTable_Lookup(t *testing.T) {
	ht := NewHandleTable()
	dev := dgxa100.NewDevice(0)

	// Register and lookup
	handle := ht.Register(dev)
	retrieved := ht.Lookup(handle)
	require.Equal(t, dev, retrieved, "Lookup returned different device")

	// Lookup invalid handle - returns InvalidDeviceInstance (null-object pattern)
	invalid := ht.Lookup(999)
	require.Equal(t, InvalidDeviceInstance, invalid, "Expected InvalidDeviceInstance for invalid handle")

	// Lookup zero handle - returns InvalidDeviceInstance
	zero := ht.Lookup(0)
	require.Equal(t, InvalidDeviceInstance, zero, "Expected InvalidDeviceInstance for zero handle")
}

func TestHandleTable_Clear(t *testing.T) {
	ht := NewHandleTable()
	dev1 := dgxa100.NewDevice(0)
	dev2 := dgxa100.NewDevice(1)

	handle1 := ht.Register(dev1)
	handle2 := ht.Register(dev2)

	require.Equal(t, 2, ht.Count(), "Expected count 2")

	ht.Clear()

	require.Equal(t, 0, ht.Count(), "Expected count 0 after clear")

	// Lookup should return InvalidDeviceInstance after clear (null-object pattern)
	require.Equal(t, InvalidDeviceInstance, ht.Lookup(handle1), "Expected InvalidDeviceInstance after clear")
	require.Equal(t, InvalidDeviceInstance, ht.Lookup(handle2), "Expected InvalidDeviceInstance after clear")
}

func TestHandleTable_MultipleDevices(t *testing.T) {
	ht := NewHandleTable()
	// Use MaxDevices to respect the handle table limit
	devices := make([]nvml.Device, MaxDevices)
	handles := make([]uintptr, MaxDevices)

	// Register multiple devices
	for i := 0; i < MaxDevices; i++ {
		devices[i] = dgxa100.NewDevice(i)
		handles[i] = ht.Register(devices[i])
	}

	require.Equal(t, MaxDevices, ht.Count(), "Expected count %d", MaxDevices)

	// Verify all handles are unique
	seen := make(map[uintptr]bool)
	for _, h := range handles {
		require.False(t, seen[h], "Duplicate handle detected: %d", h)
		seen[h] = true
	}

	// Verify all lookups work
	for i, h := range handles {
		retrieved := ht.Lookup(h)
		require.Equal(t, devices[i], retrieved, "Lookup failed for device %d", i)
	}
}

func TestHandleTable_ConcurrentAccess(t *testing.T) {
	ht := NewHandleTable()
	var wg sync.WaitGroup
	var successCount int32
	numGoroutines := 100

	// Concurrent registration - each goroutine tries to register one unique device
	// Only MaxDevices will succeed due to handle table limit
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			dev := dgxa100.NewDevice(id)
			handle := ht.Register(dev)
			if handle != 0 {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}
	wg.Wait()

	// Due to MaxDevices limit, only MaxDevices registrations should succeed
	require.Equal(t, int32(MaxDevices), successCount, "Expected %d successful registrations", MaxDevices)

	require.Equal(t, MaxDevices, ht.Count(), "Expected count %d", MaxDevices)
}

func TestHandleTable_ConcurrentRegisterAndLookup(t *testing.T) {
	ht := NewHandleTable()
	var wg sync.WaitGroup
	var lookupNilCount int32
	numGoroutines := 50

	// Pre-register devices up to MaxDevices limit
	handles := make([]uintptr, MaxDevices)
	devices := make([]nvml.Device, MaxDevices)
	for i := 0; i < MaxDevices; i++ {
		devices[i] = dgxa100.NewDevice(i)
		handles[i] = ht.Register(devices[i])
	}

	// Concurrent lookups only - table is already at capacity
	// Testing that lookups work correctly under concurrent access
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				handle := handles[j%len(handles)]
				dev := ht.Lookup(handle)
				if dev == nil {
					atomic.AddInt32(&lookupNilCount, 1)
				}
			}
		}()
	}
	wg.Wait()

	require.Zero(t, lookupNilCount, "Lookup returned nil %d times for valid handles", lookupNilCount)
}

func TestHandleTable_ConcurrentClear(t *testing.T) {
	ht := NewHandleTable()
	var wg sync.WaitGroup

	// Register devices up to MaxDevices limit
	for i := 0; i < MaxDevices; i++ {
		dev := dgxa100.NewDevice(i)
		ht.Register(dev)
	}

	// Concurrent clear and operations
	wg.Add(3)
	go func() {
		defer wg.Done()
		ht.Clear()
	}()
	go func() {
		defer wg.Done()
		dev := dgxa100.NewDevice(100)
		ht.Register(dev)
	}()
	go func() {
		defer wg.Done()
		ht.Lookup(1)
	}()
	wg.Wait()

	// Should not crash - that's the main test
}
