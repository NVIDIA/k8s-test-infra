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

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// HandleTable manages the mapping between C handles (uintptr) and Go device
// objects. This is necessary because CGo doesn't allow passing Go pointers
// with nested Go pointers to C code.
type HandleTable struct {
	devices    map[uintptr]nvml.Device
	reverse    map[nvml.Device]uintptr
	nextHandle atomic.Uintptr // monotonic counter for unique handles
	mu         sync.RWMutex
}

// NewHandleTable creates a new HandleTable.
func NewHandleTable() *HandleTable {
	ht := &HandleTable{
		devices: make(map[uintptr]nvml.Device),
		reverse: make(map[nvml.Device]uintptr),
	}
	ht.nextHandle.Store(1) // Start from 1, 0 reserved for NULL
	return ht
}

// Register adds a device to the handle table and returns its handle.
// If the device is already registered, returns the existing handle.
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

	// Allocate new handle using atomic counter (prevents reuse after Clear)
	handle := ht.nextHandle.Add(1) - 1
	ht.devices[handle] = dev
	ht.reverse[dev] = handle
	return handle
}

// Lookup returns the device for the given handle.
// Returns nil if the handle is invalid.
func (ht *HandleTable) Lookup(handle uintptr) nvml.Device {
	if handle == 0 {
		return nil
	}

	ht.mu.RLock()
	defer ht.mu.RUnlock()
	return ht.devices[handle]
}

// Clear removes all entries from the handle table.
func (ht *HandleTable) Clear() {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	ht.devices = make(map[uintptr]nvml.Device)
	ht.reverse = make(map[nvml.Device]uintptr)
}

// Count returns the number of registered handles.
func (ht *HandleTable) Count() int {
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	return len(ht.devices)
}
