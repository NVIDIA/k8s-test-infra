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
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	"github.com/stretchr/testify/require"
)

// unregisteredHandle returns a valid, non-nil pointer that no HandleTable has
// ever issued.
//
// Negative-path tests used to fabricate handles from integer literals
// (Lookup(999)). Now that a handle is an unsafe.Pointer, that form is only
// expressible as unsafe.Pointer(uintptr(999)) -- itself exactly the "possible
// misuse of unsafe.Pointer" this package was cleaned of, and a real hazard
// under -race's checkptr instrumentation. Backing the handle with a genuine Go
// allocation keeps the pointer legal while still landing on the branch under
// test: an address absent from the table.
func unregisteredHandle() unsafe.Pointer {
	return unsafe.Pointer(new([64]byte))
}

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

	// Lookup a handle this table never issued - returns InvalidDeviceInstance
	// (null-object pattern)
	invalid := ht.Lookup(unregisteredHandle())
	require.Equal(t, InvalidDeviceInstance, invalid, "Expected InvalidDeviceInstance for invalid handle")

	// Lookup nil handle - returns InvalidDeviceInstance
	zero := ht.Lookup(nil)
	require.Equal(t, InvalidDeviceInstance, zero, "Expected InvalidDeviceInstance for nil handle")
}

// TestHandleTable_LookupRejectsForgedHandle pins where the handle table's
// trust boundary sits: membership in the table authorises a handle, NOT the
// magic number in the block it points at.
//
// This matters because the bridge forwards whatever pointer the C caller
// supplied -- every nvmlDeviceGetX(nvmlDevice_t device, ...) export reads
// device.handle straight out of the caller's struct and passes it to
// LookupDevice. So an address that merely looks like a HandleBlock has to be
// refused.
//
// The block below carries the exact magic value isValidHandle checks for.
// With the map-membership check in place, Lookup returns before C is ever
// reached. Remove that check, or reorder it after the C validation on the
// theory that the magic number is sufficient, and this forged handle passes
// isValidHandle; Lookup then returns the map's zero value -- a nil
// nvml.Device -- instead of InvalidDeviceInstance, and every bridge caller
// nil-derefs where it should have returned ERROR_INVALID_ARGUMENT.
func TestHandleTable_LookupRejectsForgedHandle(t *testing.T) {
	ht := NewHandleTable()
	defer ht.Clear()

	// A real registration, so the table is populated and the forged lookup
	// has to actually miss rather than hit an empty map.
	require.NotNil(t, ht.Register(dgxa100.NewDevice(0)), "setup: Register must succeed")

	// Mimic the C HandleBlock prefix: a 4-byte magic field at offset 0
	// holding "NVML" (0x4E564D4C), in native byte order.
	forged := make([]byte, 64)
	binary.NativeEndian.PutUint32(forged[:4], 0x4E564D4C)

	got := ht.Lookup(unsafe.Pointer(&forged[0]))
	require.Equal(t, InvalidDeviceInstance, got,
		"a pointer the table never issued must be rejected even when it carries a valid magic number")
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
	handles := make([]unsafe.Pointer, MaxDevices)

	// Register multiple devices
	for i := 0; i < MaxDevices; i++ {
		devices[i] = dgxa100.NewDevice(i)
		handles[i] = ht.Register(devices[i])
	}

	require.Equal(t, MaxDevices, ht.Count(), "Expected count %d", MaxDevices)

	// Verify all handles are unique
	seen := make(map[unsafe.Pointer]bool)
	for _, h := range handles {
		require.False(t, seen[h], "Duplicate handle detected: %p", h)
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
			if handle != nil {
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
	handles := make([]unsafe.Pointer, MaxDevices)
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
	late := dgxa100.NewDevice(100)
	wg.Add(3)
	go func() {
		defer wg.Done()
		ht.Clear()
	}()
	go func() {
		defer wg.Done()
		ht.Register(late)
	}()
	go func() {
		defer wg.Done()
		ht.Lookup(unregisteredHandle())
	}()
	wg.Wait()

	// Clear() and Register() serialise on the mutex, so exactly two orderings
	// are reachable: Register first (table already at MaxDevices, so it is
	// refused) then Clear wipes everything -> 0 entries; or Clear first then
	// Register admits `late` -> 1 entry. Any other count means a pre-Clear
	// entry survived, i.e. the two operations interleaved instead of
	// serialising.
	count := ht.Count()
	require.LessOrEqual(t, count, 1,
		"Clear() must not interleave with Register(): at most the one late device may survive, got %d", count)

	// Whichever ordering won, devices and reverse must still agree with each
	// other -- Clear() resets both maps, so a surviving entry has to round-trip
	// device -> handle -> device.
	if count == 1 {
		h := ht.HandleFor(late)
		require.NotNil(t, h, "surviving device must still resolve to a handle")
		require.Equal(t, late, ht.Lookup(h), "surviving handle must resolve back to its device")
	}
}
