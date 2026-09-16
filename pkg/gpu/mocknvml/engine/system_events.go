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
	"sync"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// The two event types NVML's system event set carries. Unlike the per-device
// mask, which enumerates Xid classes, the system set reports only whether a
// GPU is bound to the driver.
const (
	SystemEventTypeGpuDriverUnbind uint64 = 0x1
	SystemEventTypeGpuDriverBind   uint64 = 0x2
)

// maxQueuedSystemEvents bounds a single set's backlog. A client that registers
// a set and then stops waiting must not grow the queue without limit; the
// oldest entries are dropped first so a consumer that comes back still learns
// the current state of the node rather than replaying ancient history.
const maxQueuedSystemEvents = 64

// SystemEvent is one driver bind/unbind transition. GpuID carries the PCI-format
// GPU id NVML puts in nvmlSystemEventData_v1_t.
type SystemEvent struct {
	Type  uint64
	GpuID uint32
}

// systemEventSet is one live nvmlSystemEventSet_t: the mask the client
// registered plus the transitions that have accrued since it last waited.
type systemEventSet struct {
	mask  uint64
	queue []SystemEvent
}

// systemEventBroker derives GPU driver bind/unbind events by watching each
// device's driver-bound state, and fans every transition out to the event sets
// clients have registered.
//
// The mock runs no background thread — a library loaded into an arbitrary
// consumer process must not spawn one — so state is sampled on the wait path,
// the same way PendingXidEvent samples the failure injectors. The consequence
// is that only transitions visible between two samples are reported: a GPU that
// is failed and recovered inside one poll interval nets out to no event. A
// consumer looping on the wait samples every waitPollInterval, well inside the
// config override poll TTL that drives the transitions in the first place.
type systemEventBroker struct {
	mu sync.Mutex

	// sets is keyed by the opaque token the bridge allocated for the set, so
	// C memory ownership stays with the bridge exactly as it does for the
	// per-device nvmlEventSetCreate.
	sets map[unsafe.Pointer]*systemEventSet

	// bound records the driver-bound state each device was last sampled in.
	bound map[int]bool

	// seeded suppresses events for the very first sample. Without it every
	// device would appear to have just been bound, and a consumer would see a
	// burst of bind events for GPUs that never transitioned.
	seeded bool
}

func newSystemEventBroker() *systemEventBroker {
	return &systemEventBroker{
		sets:  make(map[unsafe.Pointer]*systemEventSet),
		bound: make(map[int]bool),
	}
}

// create registers a set under token. A duplicate token is rejected: the bridge
// allocates a fresh one per create, so a collision means the bridge leaked.
func (b *systemEventBroker) create(token unsafe.Pointer) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.sets[token]; exists {
		return false
	}
	b.sets[token] = &systemEventSet{}
	return true
}

func (b *systemEventBroker) free(token unsafe.Pointer) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.sets[token]; !exists {
		return false
	}
	delete(b.sets, token)
	return true
}

// register adds mask to the set's registered event types. NVML accumulates
// across calls rather than replacing, so a client can subscribe to bind and
// unbind in two calls.
func (b *systemEventBroker) register(token unsafe.Pointer, mask uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	set, exists := b.sets[token]
	if !exists {
		return false
	}
	set.mask |= mask
	return true
}

// sample compares the driver-bound state of every device against the last
// observation and enqueues a transition on each set subscribed to it.
func (b *systemEventBroker) sample(states map[int]uint32, bound map[int]bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for index, isBound := range bound {
		was, known := b.bound[index]
		b.bound[index] = isBound
		if !b.seeded || (known && was == isBound) {
			continue
		}
		// A device that appears for the first time after seeding (container
		// gained a /dev/nvidia node) is reported as a bind, which is what the
		// transition actually is.
		eventType := SystemEventTypeGpuDriverUnbind
		if isBound {
			eventType = SystemEventTypeGpuDriverBind
		}
		b.enqueueLocked(SystemEvent{Type: eventType, GpuID: states[index]})
	}
	b.seeded = true
}

func (b *systemEventBroker) enqueueLocked(event SystemEvent) {
	for _, set := range b.sets {
		if set.mask&event.Type == 0 {
			continue
		}
		set.queue = append(set.queue, event)
		if len(set.queue) > maxQueuedSystemEvents {
			set.queue = set.queue[len(set.queue)-maxQueuedSystemEvents:]
		}
	}
}

// drain removes and returns up to limit queued events for the set. A set with
// an empty queue returns nil, which the bridge reports as "no events yet".
func (b *systemEventBroker) drain(token unsafe.Pointer, limit int) ([]SystemEvent, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	set, exists := b.sets[token]
	if !exists {
		return nil, false
	}
	if limit <= 0 || len(set.queue) == 0 {
		return nil, true
	}
	limit = min(limit, len(set.queue))
	out := set.queue[:limit:limit]
	set.queue = set.queue[limit:]
	return out, true
}

// SystemEventSetCreate registers a system event set keyed by the opaque token
// the bridge allocated for it.
func (e *Engine) SystemEventSetCreate(token unsafe.Pointer) nvml.Return {
	if token == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if !e.systemEvents.create(token) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return nvml.SUCCESS
}

// SystemEventSetFree drops a set. An unknown token is ERROR_INVALID_ARGUMENT
// rather than SUCCESS so a double free surfaces to the caller instead of
// silently passing.
func (e *Engine) SystemEventSetFree(token unsafe.Pointer) nvml.Return {
	if token == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if !e.systemEvents.free(token) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return nvml.SUCCESS
}

// SystemRegisterEvents subscribes a set to the given event types. Bits outside
// the two types NVML defines are rejected, matching a driver that validates the
// mask rather than silently recording nothing.
func (e *Engine) SystemRegisterEvents(token unsafe.Pointer, eventTypes uint64) nvml.Return {
	if token == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	known := SystemEventTypeGpuDriverBind | SystemEventTypeGpuDriverUnbind
	if eventTypes == 0 || eventTypes&^known != 0 {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if !e.systemEvents.register(token, eventTypes) {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return nvml.SUCCESS
}

// SystemEventSetPoll samples the current driver-bound state of every visible
// device and returns up to limit events queued for the set. It is the
// non-blocking core of nvmlSystemEventSetWait; the bridge owns the blocking.
func (e *Engine) SystemEventSetPoll(token unsafe.Pointer, limit int) ([]SystemEvent, nvml.Return) {
	if token == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}

	gpuIDs, bound := e.driverBindState()
	e.systemEvents.sample(gpuIDs, bound)

	events, ok := e.systemEvents.drain(token, limit)
	if !ok {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	return events, nvml.SUCCESS
}

// driverBindState reports the PCI-format GPU id and current driver-bound state
// of every visible device. A device the failure injector has lost (lost or
// fallen_off_bus) is reported unbound: that is the same condition the handle
// lookups and nvmlEventSetWait already treat as a GPU off the bus, so failure
// injection and the system event stream cannot disagree.
func (e *Engine) driverBindState() (gpuIDs map[int]uint32, bound map[int]bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	gpuIDs = make(map[int]uint32)
	bound = make(map[int]bool)
	if e.initCount == 0 || e.server == nil {
		return gpuIDs, bound
	}

	for index, dev := range e.server.configurableDevices {
		if dev == nil || !e.server.isDeviceVisible(index) {
			continue
		}
		gpuIDs[index] = dev.boardID
		bound[index] = !dev.failureLostByConfig()
	}
	return gpuIDs, bound
}
