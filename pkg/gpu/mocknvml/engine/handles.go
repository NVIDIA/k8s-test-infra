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

/*
#include <stdlib.h>

// Allocate a handle block - a small C struct that nvidia-smi can dereference
// without crashing. The actual device lookup happens in Go via the handle table.
typedef struct {
    unsigned int magic;      // Magic number for validation
    unsigned int index;      // Device index
    void* reserved[4];       // Reserved space that might be read
} HandleBlock;

static void* allocHandle(unsigned int index) {
    HandleBlock* block = (HandleBlock*)calloc(1, sizeof(HandleBlock));
    if (block) {
        block->magic = 0x4E564D4C;  // "NVML"
        block->index = index;
    }
    return (void*)block;
}

static void freeHandle(void* handle) {
    free(handle);
}

// isValidHandle checks if the handle has the correct magic number.
// Returns 1 if valid, 0 otherwise.
static int isValidHandle(void* handle) {
    if (handle == NULL) {
        return 0;
    }
    HandleBlock* block = (HandleBlock*)handle;
    return block->magic == 0x4E564D4C;  // "NVML"
}
*/
import "C"
import (
	"sync"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// HandleTable manages the mapping between C handles and Go device objects.
// This is necessary because CGo doesn't allow passing Go pointers with nested
// Go pointers to C code.
//
// Handles are C-allocated memory blocks (see allocHandle above) that
// nvidia-smi can safely dereference. They are typed unsafe.Pointer rather
// than uintptr on purpose: a handle IS a pointer, it is dereferenced by
// isValidHandle and by the C caller, and uintptr is documented as an integer
// that does not hold a reference. Keeping the pointer type end to end means
// no code path ever has to convert an integer back into a pointer, which is
// both the honest model and what lets `go vet`'s unsafeptr check pass without
// suppression.
//
// The memory these handles point at is libc heap, not Go heap, so the Go
// garbage collector never scans, moves or frees it; the C block outlives any
// Go reference and is released only by Clear().
//
// LIFECYCLE:
//   - Handles are allocated via Register() when devices are first accessed
//   - Handles persist until Clear() is called (typically on Shutdown)
//   - Individual handle deallocation is NOT supported (matches NVML behavior)
//   - Maximum of MaxDevices handles can be registered
//
// THREAD SAFETY:
//   - All methods are thread-safe via RWMutex
//   - Lookup() validates handle magic number to detect use-after-free
type HandleTable struct {
	devices map[unsafe.Pointer]nvml.Device
	reverse map[nvml.Device]unsafe.Pointer
	mu      sync.RWMutex
}

// NewHandleTable creates a new HandleTable.
func NewHandleTable() *HandleTable {
	return &HandleTable{
		devices: make(map[unsafe.Pointer]nvml.Device),
		reverse: make(map[nvml.Device]unsafe.Pointer),
	}
}

// Register adds a device to the handle table and returns its handle.
// If the device is already registered, returns the existing handle.
// The handle is a pointer to C-allocated memory.
func (ht *HandleTable) Register(dev nvml.Device) unsafe.Pointer {
	if dev == nil {
		return nil
	}

	ht.mu.Lock()
	defer ht.mu.Unlock()

	// Check if already registered
	if handle, exists := ht.reverse[dev]; exists {
		return handle
	}

	// Check bounds to prevent device index overflow
	if len(ht.devices) >= MaxDevices {
		return nil
	}

	// Allocate a C handle block with device index. cgo maps C's void* to
	// unsafe.Pointer, so the handle is already correctly typed here.
	deviceIndex := uint32(len(ht.devices))
	handle := C.allocHandle(C.uint(deviceIndex))
	if handle == nil {
		// Memory allocation failed
		return nil
	}

	ht.devices[handle] = dev
	ht.reverse[dev] = handle
	return handle
}

// Lookup returns the device for the given handle.
// Returns InvalidDeviceInstance if the handle is invalid (null-object pattern).
// This eliminates nil checks in the bridge layer - callers can safely call
// methods on the returned device; invalid devices return ERROR_INVALID_ARGUMENT.
func (ht *HandleTable) Lookup(handle unsafe.Pointer) nvml.Device {
	if handle == nil {
		return InvalidDeviceInstance
	}

	// First check if handle exists in our map - this avoids calling C code
	// with arbitrary invalid pointers which can trigger Go's checkptr panic.
	// The bridge passes through whatever pointer the C caller supplied, so
	// this map check is the trust boundary: an address we never handed out
	// is rejected before anything dereferences it.
	ht.mu.RLock()
	dev, ok := ht.devices[handle]
	ht.mu.RUnlock()

	if !ok {
		return InvalidDeviceInstance
	}

	// Validate the handle's magic number to detect use-after-free or corruption
	if C.isValidHandle(handle) == 0 {
		return InvalidDeviceInstance
	}

	return dev
}

// HandleFor returns the registered handle for the given device, or nil when
// the device has not yet been registered. Used by event-set wiring that
// needs to translate a tripped device back to a handle the caller
// already holds (e.g. the Xid critical-error event delivery path). Real
// NVML accepts events for devices the caller has resolved a handle for;
// returning nil here lets the caller treat that case as "no event yet".
func (ht *HandleTable) HandleFor(dev nvml.Device) unsafe.Pointer {
	if dev == nil {
		return nil
	}
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	return ht.reverse[dev]
}

// Clear removes all entries from the handle table and frees allocated memory.
func (ht *HandleTable) Clear() {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	// Free all allocated C handle blocks
	for handle := range ht.devices {
		C.freeHandle(handle)
	}

	ht.devices = make(map[unsafe.Pointer]nvml.Device)
	ht.reverse = make(map[nvml.Device]unsafe.Pointer)
}

// Count returns the number of registered handles.
func (ht *HandleTable) Count() int {
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	return len(ht.devices)
}
