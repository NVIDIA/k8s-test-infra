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

// retireHandle invalidates a handle block without freeing it, so its
// address can never be handed out again for a later registration.
static void retireHandle(void* handle) {
    if (handle) {
        ((HandleBlock*)handle)->magic = 0;
    }
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
	handleStore[nvml.Device]
}

// maxMigDevicesPerGPU is the most MIG devices one board can be partitioned
// into: eight GPU instances, each with a compute instance. It is the NVML
// ceiling, not a mock limit.
const maxMigDevicesPerGPU = 8

// maxDeviceHandles caps the device handle table.
//
// MIG devices share the table with physical GPUs — NVML hands both out as
// nvmlDevice_t — so the capacity has to cover a fully partitioned node. Sizing
// it to MaxDevices alone means the GPUs alone exhaust the table and the first
// MIG device fails to register.
const maxDeviceHandles = MaxDevices * (1 + maxMigDevicesPerGPU)

// NewHandleTable creates a new HandleTable.
func NewHandleTable() *HandleTable {
	table := &HandleTable{}
	table.init(maxDeviceHandles)
	return table
}

// Lookup returns the device for the given handle.
// Returns InvalidDeviceInstance if the handle is invalid (null-object pattern).
// This eliminates nil checks in the bridge layer - callers can safely call
// methods on the returned device; invalid devices return ERROR_INVALID_ARGUMENT.
func (ht *HandleTable) Lookup(handle unsafe.Pointer) nvml.Device {
	dev, ok := ht.lookup(handle)
	if !ok {
		return InvalidDeviceInstance
	}
	return dev
}

// GpuInstanceTable maps C handles to MIG GPU instances.
//
// It is separate from the device table because NVML hands callers an
// nvmlGpuInstance_t that is distinct from a device handle: a caller creates an
// instance, keeps its handle, and later partitions or destroys through it.
//
// Unlike devices, instances are destroyed while the library stays loaded, so
// this table supports Retire — see handleStore.Retire for why a destroyed
// instance's block is never reused.
type GpuInstanceTable struct{ handleStore[nvml.GpuInstance] }

// ComputeInstanceTable maps C handles to MIG compute instances, playing the
// same role for nvmlComputeInstance_t that GpuInstanceTable does for GPU
// instances.
type ComputeInstanceTable struct{ handleStore[nvml.ComputeInstance] }

// maxMIGInstances caps each instance table, generously: a board offers at most
// eight GPU instances and eight compute instances within each. The cap exists
// only so a caller that creates and destroys in a loop cannot grow the table
// without bound.
const maxMIGInstances = MaxDevices * maxMigDevicesPerGPU * maxMigDevicesPerGPU

// NewGpuInstanceTable creates an empty GPU instance handle table.
func NewGpuInstanceTable() *GpuInstanceTable {
	table := &GpuInstanceTable{}
	table.init(maxMIGInstances)
	return table
}

// NewComputeInstanceTable creates an empty compute instance handle table.
func NewComputeInstanceTable() *ComputeInstanceTable {
	table := &ComputeInstanceTable{}
	table.init(maxMIGInstances)
	return table
}

// Lookup returns the GPU instance for a handle, or false when the handle is
// not one this table issued or has since been retired.
func (t *GpuInstanceTable) Lookup(handle unsafe.Pointer) (nvml.GpuInstance, bool) {
	return t.lookup(handle)
}

// Lookup returns the compute instance for a handle, or false when the handle
// is not one this table issued or has since been retired.
func (t *ComputeInstanceTable) Lookup(handle unsafe.Pointer) (nvml.ComputeInstance, bool) {
	return t.lookup(handle)
}

// handleStore is the shared machinery behind every handle table: C-allocated
// blocks, a two-way map, and the retirement policy that keeps a stale handle
// stale. It is generic because GPU and compute instance handles need exactly
// the same lifecycle as device handles, and duplicating it would mean
// duplicating the cgo preamble that allocates the blocks.
type handleStore[T comparable] struct {
	items   map[unsafe.Pointer]T
	reverse map[T]unsafe.Pointer
	// retired holds blocks handed out before a Clear or a Retire. They are
	// kept allocated (with the magic cleared) instead of freed so that a
	// later Register can never reuse their address: a client holding a handle
	// from before nvmlShutdown, or to a destroyed MIG instance, must keep
	// getting ERROR_INVALID_ARGUMENT rather than aliasing whichever object
	// happened to get the recycled block. Blocks are tiny.
	retired []unsafe.Pointer
	limit   int
	mu      sync.RWMutex
}

func (s *handleStore[T]) init(limit int) {
	s.items = make(map[unsafe.Pointer]T)
	s.reverse = make(map[T]unsafe.Pointer)
	s.limit = limit
}

// Register adds an object to the table and returns its handle, reusing the
// existing handle when the object is already registered. The handle is a
// pointer to C-allocated memory.
func (s *handleStore[T]) Register(item T) unsafe.Pointer {
	var zero T
	if item == zero {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if handle, exists := s.reverse[item]; exists {
		return handle
	}
	if len(s.items) >= s.limit {
		return nil
	}

	// Allocate a C handle block. cgo maps C's void* to unsafe.Pointer, so the
	// handle is already correctly typed here.
	handle := C.allocHandle(C.uint(len(s.items)))
	if handle == nil {
		return nil
	}

	s.items[handle] = item
	s.reverse[item] = handle
	return handle
}

// lookup resolves a handle, reporting whether it is one this table still
// vouches for.
func (s *handleStore[T]) lookup(handle unsafe.Pointer) (T, bool) {
	var zero T
	if handle == nil {
		return zero, false
	}

	// Check the map before touching the block: this avoids calling C code
	// with arbitrary invalid pointers, which can trigger Go's checkptr panic.
	// The bridge passes through whatever pointer the C caller supplied, so
	// this map check is the trust boundary — an address we never handed out
	// is rejected before anything dereferences it.
	s.mu.RLock()
	item, ok := s.items[handle]
	s.mu.RUnlock()

	if !ok {
		return zero, false
	}
	// Validate the magic number to detect use-after-free or corruption.
	if C.isValidHandle(handle) == 0 {
		return zero, false
	}
	return item, true
}

// HandleFor returns the registered handle for an object, or nil when it has
// not been registered. Used by wiring that needs to translate an object back
// into a handle the caller already holds (e.g. the Xid critical-error event
// delivery path). Real NVML accepts events for devices the caller has
// resolved a handle for; returning nil lets the caller treat that case as
// "no event yet".
func (s *handleStore[T]) HandleFor(item T) unsafe.Pointer {
	var zero T
	if item == zero {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reverse[item]
}

// Retire invalidates the handle for a single object, for the MIG instances
// that are destroyed while the library stays loaded. The block is retired
// rather than freed, so the caller's now-stale handle keeps failing lookup
// instead of aliasing a later instance that reused the address.
func (s *handleStore[T]) Retire(item T) {
	var zero T
	if item == zero {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	handle, ok := s.reverse[item]
	if !ok {
		return
	}
	C.retireHandle(handle)
	s.retired = append(s.retired, handle)
	delete(s.items, handle)
	delete(s.reverse, item)
}

// Clear invalidates every registered handle. The C blocks are retired, not
// freed (see handleStore.retired), so handles issued before the Clear stay
// invalid for the lifetime of the table even after new registrations.
func (s *handleStore[T]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for handle := range s.items {
		C.retireHandle(handle)
		s.retired = append(s.retired, handle)
	}

	s.items = make(map[unsafe.Pointer]T)
	s.reverse = make(map[T]unsafe.Pointer)
}

// Free releases every block, live and retired. Only for tests and teardown;
// any handle a client still holds becomes a dangling pointer afterwards.
func (s *handleStore[T]) Free() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for handle := range s.items {
		C.freeHandle(handle)
	}
	for _, handle := range s.retired {
		C.freeHandle(handle)
	}
	s.retired = nil
	s.items = make(map[unsafe.Pointer]T)
	s.reverse = make(map[T]unsafe.Pointer)
}

// Count returns the number of registered handles.
func (s *handleStore[T]) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}
