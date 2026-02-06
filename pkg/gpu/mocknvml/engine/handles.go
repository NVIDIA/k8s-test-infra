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

// HandleTable manages the mapping between C handles (uintptr) and Go device
// objects. This is necessary because CGo doesn't allow passing Go pointers
// with nested Go pointers to C code.
//
// Handles are C-allocated memory blocks that nvidia-smi can safely dereference.
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
	devices map[uintptr]nvml.Device
	reverse map[nvml.Device]uintptr
	mu      sync.RWMutex
}

// NewHandleTable creates a new HandleTable.
func NewHandleTable() *HandleTable {
	return &HandleTable{
		devices: make(map[uintptr]nvml.Device),
		reverse: make(map[nvml.Device]uintptr),
	}
}

// Register adds a device to the handle table and returns its handle.
// If the device is already registered, returns the existing handle.
// The handle is a pointer to C-allocated memory.
func (ht *HandleTable) Register(dev nvml.Device) uintptr {
	if dev == nil {
		return 0
	}

	ht.mu.Lock()
	defer ht.mu.Unlock()

	// Check if already registered
	if handle, exists := ht.reverse[dev]; exists {
		return handle
	}

	// Check bounds to prevent device index overflow
	if len(ht.devices) >= MaxDevices {
		return 0
	}

	// Allocate a C handle block with device index
	deviceIndex := uint32(len(ht.devices))
	cHandle := C.allocHandle(C.uint(deviceIndex))
	if cHandle == nil {
		// Memory allocation failed
		return 0
	}
	handle := uintptr(unsafe.Pointer(cHandle))

	ht.devices[handle] = dev
	ht.reverse[dev] = handle
	return handle
}

// Lookup returns the device for the given handle.
// Returns InvalidDeviceInstance if the handle is invalid (null-object pattern).
// This eliminates nil checks in the bridge layer - callers can safely call
// methods on the returned device; invalid devices return ERROR_INVALID_ARGUMENT.
func (ht *HandleTable) Lookup(handle uintptr) nvml.Device {
	if handle == 0 {
		return InvalidDeviceInstance
	}

	// First check if handle exists in our map - this avoids calling C code
	// with arbitrary invalid pointers which can trigger Go's checkptr panic
	ht.mu.RLock()
	dev, ok := ht.devices[handle]
	ht.mu.RUnlock()

	if !ok {
		return InvalidDeviceInstance
	}

	// Validate the handle's magic number to detect use-after-free or corruption
	//nolint:govet // Converting uintptr to unsafe.Pointer is intentional -
	// handle was allocated as C memory and we need to validate it
	if C.isValidHandle(unsafe.Pointer(handle)) == 0 {
		return InvalidDeviceInstance
	}

	return dev
}

// Clear removes all entries from the handle table and frees allocated memory.
func (ht *HandleTable) Clear() {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	// Free all allocated C handle blocks
	for handle := range ht.devices {
		//nolint:govet // Converting uintptr back to unsafe.Pointer is intentional here -
		// the handle was originally allocated as C memory and stored as uintptr for map key use
		C.freeHandle(unsafe.Pointer(handle))
	}

	ht.devices = make(map[uintptr]nvml.Device)
	ht.reverse = make(map[nvml.Device]uintptr)
}

// Count returns the number of registered handles.
func (ht *HandleTable) Count() int {
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	return len(ht.devices)
}
