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
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// token mints a distinct non-nil pointer to stand in for the C allocation the
// bridge makes per event set. The broker only ever compares pointer values.
func token(t *testing.T) unsafe.Pointer {
	t.Helper()
	slot := new(byte)
	return unsafe.Pointer(slot)
}

const bothSystemEventTypes = SystemEventTypeGpuDriverBind | SystemEventTypeGpuDriverUnbind

// =============================================================================
// Broker unit tests (no engine wiring)
// =============================================================================

// TestBrokerFirstSampleIsSilent pins the seeding rule. Without it every GPU
// would look newly bound on the first sample and a consumer would open with a
// burst of bind events for hardware that never transitioned.
func TestBrokerFirstSampleIsSilent(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	set := token(t)
	require.True(t, broker.create(set))
	require.True(t, broker.register(set, bothSystemEventTypes))

	broker.sample(map[int]uint32{0: 0x100, 1: 0x200}, map[int]bool{0: true, 1: true})

	events, ok := broker.drain(set, 8)
	require.True(t, ok)
	require.Empty(t, events, "the first sample establishes the baseline and must emit nothing")
}

func TestBrokerReportsTransitionsInBothDirections(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	set := token(t)
	require.True(t, broker.create(set))
	require.True(t, broker.register(set, bothSystemEventTypes))

	gpuIDs := map[int]uint32{0: 0x100, 1: 0x200}
	broker.sample(gpuIDs, map[int]bool{0: true, 1: true})

	broker.sample(gpuIDs, map[int]bool{0: true, 1: false})
	events, ok := broker.drain(set, 8)
	require.True(t, ok)
	require.Equal(t, []SystemEvent{{Type: SystemEventTypeGpuDriverUnbind, GpuID: 0x200}}, events)

	broker.sample(gpuIDs, map[int]bool{0: true, 1: true})
	events, ok = broker.drain(set, 8)
	require.True(t, ok)
	require.Equal(t, []SystemEvent{{Type: SystemEventTypeGpuDriverBind, GpuID: 0x200}}, events)
}

// TestBrokerHoldsStateBetweenSamples guards against re-reporting a device that
// is still in the state it was last seen in: a consumer looping on the wait
// would otherwise see the same unbind on every poll.
func TestBrokerHoldsStateBetweenSamples(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	set := token(t)
	require.True(t, broker.create(set))
	require.True(t, broker.register(set, bothSystemEventTypes))

	gpuIDs := map[int]uint32{0: 0x100}
	broker.sample(gpuIDs, map[int]bool{0: true})
	broker.sample(gpuIDs, map[int]bool{0: false})
	_, ok := broker.drain(set, 8)
	require.True(t, ok)

	broker.sample(gpuIDs, map[int]bool{0: false})
	events, ok := broker.drain(set, 8)
	require.True(t, ok)
	require.Empty(t, events, "a device that has not changed state must not re-emit")
}

func TestBrokerMaskFiltersEventTypes(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	bindOnly, unbindOnly := token(t), token(t)
	for _, set := range []unsafe.Pointer{bindOnly, unbindOnly} {
		require.True(t, broker.create(set))
	}
	require.True(t, broker.register(bindOnly, SystemEventTypeGpuDriverBind))
	require.True(t, broker.register(unbindOnly, SystemEventTypeGpuDriverUnbind))

	gpuIDs := map[int]uint32{0: 0x100}
	broker.sample(gpuIDs, map[int]bool{0: true})
	broker.sample(gpuIDs, map[int]bool{0: false})

	events, ok := broker.drain(bindOnly, 8)
	require.True(t, ok)
	require.Empty(t, events, "a set registered for bind only must not receive an unbind")

	events, ok = broker.drain(unbindOnly, 8)
	require.True(t, ok)
	require.Len(t, events, 1)
}

// TestBrokerFansOutToEverySet is what separates the system set from the
// per-device Xid path: an Xid is claimed once for the whole process, but every
// registered system set must see every transition independently.
func TestBrokerFansOutToEverySet(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	first, second := token(t), token(t)
	for _, set := range []unsafe.Pointer{first, second} {
		require.True(t, broker.create(set))
		require.True(t, broker.register(set, bothSystemEventTypes))
	}

	gpuIDs := map[int]uint32{0: 0x100}
	broker.sample(gpuIDs, map[int]bool{0: true})
	broker.sample(gpuIDs, map[int]bool{0: false})

	for _, set := range []unsafe.Pointer{first, second} {
		events, ok := broker.drain(set, 8)
		require.True(t, ok)
		require.Len(t, events, 1, "every registered set must receive the transition")
	}
}

// TestBrokerDropsOldestPastCap keeps a set that nobody drains from growing
// without bound. The newest transitions are the ones worth keeping: they
// describe the current state of the node.
func TestBrokerDropsOldestPastCap(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	set := token(t)
	require.True(t, broker.create(set))
	require.True(t, broker.register(set, bothSystemEventTypes))

	gpuIDs := map[int]uint32{0: 0x100}
	broker.sample(gpuIDs, map[int]bool{0: true})
	// Flip the device 2*cap + 1 times without draining.
	for i := 0; i < 2*maxQueuedSystemEvents+1; i++ {
		broker.sample(gpuIDs, map[int]bool{0: i%2 == 0})
	}

	events, ok := broker.drain(set, 4*maxQueuedSystemEvents)
	require.True(t, ok)
	require.Len(t, events, maxQueuedSystemEvents)
	// The last flip left the device unbound on an odd final index, so the
	// newest retained event must be the one that took it there.
	require.Equal(t, SystemEventTypeGpuDriverBind, events[len(events)-1].Type)
}

func TestBrokerDrainHonoursMax(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	set := token(t)
	require.True(t, broker.create(set))
	require.True(t, broker.register(set, bothSystemEventTypes))

	gpuIDs := map[int]uint32{0: 0x100, 1: 0x200, 2: 0x300}
	broker.sample(gpuIDs, map[int]bool{0: true, 1: true, 2: true})
	broker.sample(gpuIDs, map[int]bool{0: false, 1: false, 2: false})

	first, ok := broker.drain(set, 2)
	require.True(t, ok)
	require.Len(t, first, 2)
	rest, ok := broker.drain(set, 8)
	require.True(t, ok)
	require.Len(t, rest, 1, "the remainder must stay queued rather than being dropped")
}

func TestBrokerRejectsUnknownToken(t *testing.T) {
	t.Parallel()
	broker := newSystemEventBroker()
	stale := token(t)
	require.False(t, broker.register(stale, bothSystemEventTypes))
	require.False(t, broker.free(stale))
	_, ok := broker.drain(stale, 1)
	require.False(t, ok)
}

// =============================================================================
// Engine-level tests: injection through the config override document
// =============================================================================

// newSystemEventEngine returns an initialised engine plus the override path a
// test writes to, mirroring what `nvml-mock-ctl fail` does at runtime.
func newSystemEventEngine(t *testing.T, devices int) (*Engine, string, *time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	now := time.Unix(0, 0)
	clock := &now
	configOverrides = newConfigOverrideStoreAt(func() string { return path }, func() time.Time { return *clock })
	t.Cleanup(resetConfigOverrideStoreForTesting)

	e := NewEngine(&Config{NumDevices: devices, DriverVersion: "580.65.06"})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	return e, path, clock
}

// deviceGpuID is the PCI-format id the engine reports for a default device:
// createDefaultDevices addresses index i at bus i+1, and deriveBoardID packs
// the bus into the second byte.
func deviceGpuID(index int) uint32 {
	return uint32(index+1) << 8
}

func TestEngineSystemEvents_UnbindOnInjectedLoss(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 4)
	set := token(t)
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetCreate(set))
	require.Equal(t, nvml.SUCCESS, e.SystemRegisterEvents(set, bothSystemEventTypes))

	// Baseline sample: every GPU is bound, nothing to report.
	events, ret := e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, events)

	writeConfigOverride(t, path, "devices:\n  \"2\":\n    failure:\n      mode: lost\n", clock)

	events, ret = e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []SystemEvent{{
		Type:  SystemEventTypeGpuDriverUnbind,
		GpuID: deviceGpuID(2),
	}}, events, "injecting `failure: mode: lost` on GPU 2 must surface as its driver unbinding")
}

func TestEngineSystemEvents_BindWhenTheOverrideIsCleared(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 2)
	set := token(t)
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetCreate(set))
	require.Equal(t, nvml.SUCCESS, e.SystemRegisterEvents(set, bothSystemEventTypes))
	_, ret := e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, "all:\n  failure:\n    mode: fallen_off_bus\n", clock)
	events, ret := e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, events, 2, "a node-wide failure must unbind every GPU")

	require.NoError(t, os.Remove(path))
	*clock = clock.Add(2 * time.Second)
	events, ret = e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, events, 2)
	for _, event := range events {
		require.Equal(t, SystemEventTypeGpuDriverBind, event.Type)
	}
}

// TestEngineSystemEvents_AfterCallsDeviceStaysBound pins that the wait path
// never trips an injector. A device gated behind after_calls is only lost once
// a guarded getter has counted it down, so polling for system events must not
// accelerate it.
func TestEngineSystemEvents_AfterCallsDeviceStaysBound(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	set := token(t)
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetCreate(set))
	require.Equal(t, nvml.SUCCESS, e.SystemRegisterEvents(set, bothSystemEventTypes))
	_, ret := e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path,
		"devices:\n  \"0\":\n    failure:\n      mode: lost\n      after_calls: 50\n", clock)
	for i := 0; i < 5; i++ {
		events, ret := e.SystemEventSetPoll(set, 8)
		require.Equal(t, nvml.SUCCESS, ret)
		require.Empty(t, events, "an after_calls-gated device must stay bound until a getter trips it")
	}
}

func TestEngineSystemEvents_FreeThenPollIsRejected(t *testing.T) {
	e, _, _ := newSystemEventEngine(t, 1)
	set := token(t)
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetCreate(set))
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetFree(set))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemEventSetFree(set), "double free must be rejected")

	_, ret := e.SystemEventSetPoll(set, 1)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

func TestEngineSystemEvents_RegisterRejectsBadMasks(t *testing.T) {
	e, _, _ := newSystemEventEngine(t, 1)
	set := token(t)
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetCreate(set))

	for name, mask := range map[string]uint64{
		"empty":              0,
		"unknown bit":        1 << 8,
		"known plus unknown": SystemEventTypeGpuDriverBind | 1<<9,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemRegisterEvents(set, mask))
		})
	}
}

func TestEngineSystemEvents_NilTokenIsRejected(t *testing.T) {
	e, _, _ := newSystemEventEngine(t, 1)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemEventSetCreate(nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemEventSetFree(nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.SystemRegisterEvents(nil, bothSystemEventTypes))
	_, ret := e.SystemEventSetPoll(nil, 1)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

// TestEngineSystemEvents_UnregisteredSetSeesNothing covers the client that
// creates a set and waits without registering: NVML records nothing for it, so
// the queue stays empty even while GPUs transition.
func TestEngineSystemEvents_UnregisteredSetSeesNothing(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	set := token(t)
	require.Equal(t, nvml.SUCCESS, e.SystemEventSetCreate(set))
	_, ret := e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, "all:\n  failure:\n    mode: lost\n", clock)
	events, ret := e.SystemEventSetPoll(set, 8)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, events)
}
