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
)

func TestHandleTable_NewHandleTable(t *testing.T) {
	ht := NewHandleTable()
	if ht == nil {
		t.Fatal("NewHandleTable returned nil")
	}
	if ht.Count() != 0 {
		t.Errorf("Expected empty table, got count %d", ht.Count())
	}
}

func TestHandleTable_Register(t *testing.T) {
	ht := NewHandleTable()
	dev := dgxa100.NewDevice(0)

	// Register device
	handle := ht.Register(dev)
	if handle == 0 {
		t.Error("Expected non-zero handle")
	}
	if ht.Count() != 1 {
		t.Errorf("Expected count 1, got %d", ht.Count())
	}

	// Register same device again - should return same handle
	handle2 := ht.Register(dev)
	if handle != handle2 {
		t.Errorf("Expected same handle for same device, got %d and %d", handle, handle2)
	}
	if ht.Count() != 1 {
		t.Errorf("Expected count to remain 1, got %d", ht.Count())
	}
}

func TestHandleTable_RegisterNil(t *testing.T) {
	ht := NewHandleTable()
	handle := ht.Register(nil)
	if handle != 0 {
		t.Errorf("Expected 0 handle for nil device, got %d", handle)
	}
	if ht.Count() != 0 {
		t.Errorf("Expected count 0, got %d", ht.Count())
	}
}

func TestHandleTable_Lookup(t *testing.T) {
	ht := NewHandleTable()
	dev := dgxa100.NewDevice(0)

	// Register and lookup
	handle := ht.Register(dev)
	retrieved := ht.Lookup(handle)
	if retrieved != dev {
		t.Error("Lookup returned different device")
	}

	// Lookup invalid handle - returns InvalidDeviceInstance (null-object pattern)
	invalid := ht.Lookup(999)
	if invalid != InvalidDeviceInstance {
		t.Error("Expected InvalidDeviceInstance for invalid handle")
	}

	// Lookup zero handle - returns InvalidDeviceInstance
	zero := ht.Lookup(0)
	if zero != InvalidDeviceInstance {
		t.Error("Expected InvalidDeviceInstance for zero handle")
	}
}

func TestHandleTable_Clear(t *testing.T) {
	ht := NewHandleTable()
	dev1 := dgxa100.NewDevice(0)
	dev2 := dgxa100.NewDevice(1)

	handle1 := ht.Register(dev1)
	handle2 := ht.Register(dev2)

	if ht.Count() != 2 {
		t.Errorf("Expected count 2, got %d", ht.Count())
	}

	ht.Clear()

	if ht.Count() != 0 {
		t.Errorf("Expected count 0 after clear, got %d", ht.Count())
	}

	// Lookup should return InvalidDeviceInstance after clear (null-object pattern)
	if ht.Lookup(handle1) != InvalidDeviceInstance {
		t.Error("Expected InvalidDeviceInstance after clear")
	}
	if ht.Lookup(handle2) != InvalidDeviceInstance {
		t.Error("Expected InvalidDeviceInstance after clear")
	}
}

func TestHandleTable_MultipleDevices(t *testing.T) {
	ht := NewHandleTable()
	devices := make([]nvml.Device, 10)
	handles := make([]uintptr, 10)

	// Register multiple devices
	for i := 0; i < 10; i++ {
		devices[i] = dgxa100.NewDevice(i)
		handles[i] = ht.Register(devices[i])
	}

	if ht.Count() != 10 {
		t.Errorf("Expected count 10, got %d", ht.Count())
	}

	// Verify all handles are unique
	seen := make(map[uintptr]bool)
	for _, h := range handles {
		if seen[h] {
			t.Errorf("Duplicate handle detected: %d", h)
		}
		seen[h] = true
	}

	// Verify all lookups work
	for i, h := range handles {
		retrieved := ht.Lookup(h)
		if retrieved != devices[i] {
			t.Errorf("Lookup failed for device %d", i)
		}
	}
}

func TestHandleTable_ConcurrentAccess(t *testing.T) {
	ht := NewHandleTable()
	var wg sync.WaitGroup
	var zeroHandleCount int32
	numGoroutines := 100
	devicesPerGoroutine := 10

	// Concurrent registration
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < devicesPerGoroutine; j++ {
				dev := dgxa100.NewDevice(id*devicesPerGoroutine + j)
				handle := ht.Register(dev)
				if handle == 0 {
					atomic.AddInt32(&zeroHandleCount, 1)
				}
			}
		}(i)
	}
	wg.Wait()

	if zeroHandleCount > 0 {
		t.Errorf("Got %d zero handles during concurrent registration", zeroHandleCount)
	}

	expectedCount := numGoroutines * devicesPerGoroutine
	if ht.Count() != expectedCount {
		t.Errorf("Expected count %d, got %d", expectedCount, ht.Count())
	}
}

func TestHandleTable_ConcurrentRegisterAndLookup(t *testing.T) {
	ht := NewHandleTable()
	var wg sync.WaitGroup
	var lookupNilCount int32
	var registerZeroCount int32
	numGoroutines := 50

	// Pre-register some devices
	handles := make([]uintptr, 10)
	devices := make([]nvml.Device, 10)
	for i := 0; i < 10; i++ {
		devices[i] = dgxa100.NewDevice(i)
		handles[i] = ht.Register(devices[i])
	}

	// Concurrent lookups and registrations
	wg.Add(numGoroutines * 2)
	for i := 0; i < numGoroutines; i++ {
		// Lookup goroutines
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

		// Register goroutines
		go func(id int) {
			defer wg.Done()
			dev := dgxa100.NewDevice(100 + id)
			handle := ht.Register(dev)
			if handle == 0 {
				atomic.AddInt32(&registerZeroCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if lookupNilCount > 0 {
		t.Errorf("Lookup returned nil %d times for valid handles", lookupNilCount)
	}
	if registerZeroCount > 0 {
		t.Errorf("Register returned zero handle %d times", registerZeroCount)
	}
}

func TestHandleTable_ConcurrentClear(t *testing.T) {
	ht := NewHandleTable()
	var wg sync.WaitGroup

	// Register some devices
	for i := 0; i < 10; i++ {
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
