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

package main

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const bothSystemEventTypes = engine.SystemEventTypeGpuDriverBind |
	engine.SystemEventTypeGpuDriverUnbind

// newSystemEventSet creates a registered set through the exports and frees it
// on cleanup, returning the token the other hooks key off.
func newSystemEventSet(t *testing.T, eventTypes uint64) unsafe.Pointer {
	t.Helper()
	token, status := systemEventSetCreateForTest()
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.NotNil(t, token, "create must hand back a non-NULL set")
	t.Cleanup(func() { systemEventSetFreeForTest(token) })

	require.Equal(t, uint32(nvml.SUCCESS), systemRegisterEventsForTest(token, eventTypes))

	// Drain the broker's baseline sample so the test's own transition is the
	// only thing left in the queue.
	require.Equal(t, uint32(nvml.ERROR_TIMEOUT),
		systemEventWaitForTest(token, 0, 8, false).status,
		"a healthy node has nothing to report")
	return token
}

func TestSystemEventSet_CreateRegisterFreeRoundTrip(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 2, ""))

	token, status := systemEventSetCreateForTest()
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.NotNil(t, token)
	require.Equal(t, uint32(nvml.SUCCESS), systemRegisterEventsForTest(token, bothSystemEventTypes))
	require.Equal(t, uint32(nvml.SUCCESS), systemEventSetFreeForTest(token))

	// The engine must have dropped the queue with the allocation, so the
	// freed token is no longer a set anyone can register or wait on.
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), systemEventSetFreeForTest(token))
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT),
		systemRegisterEventsForTest(token, bothSystemEventTypes))
}

// TestSystemEventSet_DeliversInjectedUnbind is the end-to-end path the feature
// exists for: `nvml-mock-ctl fail --mode lost` must reach a consumer parked in
// nvmlSystemEventSetWait, with the payload written into its own array.
func TestSystemEventSet_DeliversInjectedUnbind(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithSystemEvents, 2, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	// Resolve the identities before injecting: once a GPU is lost the handle
	// lookup reports the loss instead of handing back a device.
	wantGpuIDs := []uint32{b.boardID(t, 0), b.boardID(t, 1)}

	b.fail(t, "lost")

	got := systemEventWaitForTest(token, 5_000, 8, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Len(t, got.events, 2, "both GPUs went away")

	gotGpuIDs := make([]uint32, 0, len(got.events))
	for _, event := range got.events {
		require.Equal(t, engine.SystemEventTypeGpuDriverUnbind, event.Type,
			"consumers filter on eventType; a wrong class is an undelivered event")
		gotGpuIDs = append(gotGpuIDs, event.GpuID)
	}
	require.ElementsMatch(t, wantGpuIDs, gotGpuIDs,
		"gpuId must be the PCI-format board id, the only thing naming the GPU that unbound")
}

// TestSystemEventSet_DeliversUnbindInjectedMidWait pins that the wait actually
// blocks and polls rather than sampling once: an injection that lands while a
// consumer is parked must arrive within one poll interval.
func TestSystemEventSet_DeliversUnbindInjectedMidWait(t *testing.T) {
	const injectAfter = 250 * time.Millisecond

	b := newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	var injectErr atomic.Pointer[error]
	go func() {
		time.Sleep(injectAfter)
		if err := b.writeFailure("lost"); err != nil {
			injectErr.Store(&err)
		}
	}()

	start := time.Now()
	got := systemEventWaitForTest(token, 30_000, 8, false)
	elapsed := time.Since(start)

	require.Nil(t, injectErr.Load(), "the injection itself must have succeeded")
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Len(t, got.events, 1)
	require.GreaterOrEqual(t, elapsed, injectAfter,
		"the wait must not return before the injection")
	require.Less(t, elapsed, injectAfter+waitPollInterval+schedulerSlack,
		"delivery took %s; must land within one poll interval of the injection", elapsed)
}

// TestSystemEventSet_ReportsBindWhenTheFailureClears covers the recovery half:
// nv-sentinel-class agents key remediation off the GPU coming back.
func TestSystemEventSet_ReportsBindWhenTheFailureClears(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	b.fail(t, "fallen_off_bus")
	require.Equal(t, uint32(nvml.SUCCESS), systemEventWaitForTest(token, 5_000, 8, false).status)

	b.heal(t)
	got := systemEventWaitForTest(token, 5_000, 8, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Len(t, got.events, 1)
	require.Equal(t, engine.SystemEventTypeGpuDriverBind, got.events[0].Type)
}

// TestSystemEventSet_LostGPUDoesNotFailTheWait is what separates this wait
// from nvmlEventSetWait: reporting the loss *is* the event here, so aborting
// with GPU_IS_LOST would swallow the only thing the caller asked for.
func TestSystemEventSet_LostGPUDoesNotFailTheWait(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	b.fail(t, "lost")

	got := systemEventWaitForTest(token, 5_000, 8, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status,
		"the system wait must report the unbind, not fail with GPU_IS_LOST")
	require.Len(t, got.events, 1)
}

// TestSystemEventSet_HonoursDataSize guards the caller's array: the bridge
// must never write more entries than dataSize, and the remainder must stay
// queued for the next call rather than being dropped.
func TestSystemEventSet_HonoursDataSize(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithSystemEvents, 4, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	b.fail(t, "lost")

	first := systemEventWaitForTest(token, 5_000, 1, false)
	require.Equal(t, uint32(nvml.SUCCESS), first.status)
	require.Len(t, first.events, 1, "dataSize 1 must yield exactly one event")

	rest := systemEventWaitForTest(token, 5_000, 8, false)
	require.Equal(t, uint32(nvml.SUCCESS), rest.status)
	require.Len(t, rest.events, 3, "the remaining GPUs must still be queued")
}

// TestSystemEventSet_TimeoutWhenIdle pins the millisecond conversion and the
// NVML status an idle consumer loops on.
func TestSystemEventSet_TimeoutWhenIdle(t *testing.T) {
	const timeout = 150 * time.Millisecond

	newBridgeEngine(t, profile(driverWithSystemEvents, 2, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	start := time.Now()
	got := systemEventWaitForTest(token, uint32(timeout.Milliseconds()), 8, false)
	elapsed := time.Since(start)

	require.Equal(t, uint32(nvml.ERROR_TIMEOUT), got.status)
	require.Empty(t, got.events)
	require.GreaterOrEqual(t, elapsed, timeout,
		"timeoutms must be interpreted as milliseconds and honored")
	require.Less(t, elapsed, timeout+schedulerSlack,
		"the wait must return at the timeout, not block past it")
}

// TestSystemEventSet_RejectsBadArgumentsImmediately covers every NULL and
// zero-size form. Each must fail fast rather than waiting out the timeout.
func TestSystemEventSet_RejectsBadArgumentsImmediately(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))
	token := newSystemEventSet(t, bothSystemEventTypes)

	for name, wait := range map[string]func() uint32{
		"nil data":  func() uint32 { return systemEventWaitForTest(token, 10_000, 8, true).status },
		"zero size": func() uint32 { return systemEventWaitForTest(token, 10_000, 0, false).status },
		"nil set":   func() uint32 { return systemEventWaitForTest(nil, 10_000, 8, false).status },
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			status := wait()
			require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), status)
			require.Less(t, time.Since(start), schedulerSlack,
				"an invalid call must not wait out the timeout")
		})
	}
}

func TestSystemEventSet_RejectsUnknownSet(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))

	// A plausible but never-created token: the engine keys its queues by the
	// pointer the create export handed out.
	stale := unsafe.Pointer(new(byte))
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT),
		systemRegisterEventsForTest(stale, bothSystemEventTypes))
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), systemEventSetFreeForTest(stale))

	start := time.Now()
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT),
		systemEventWaitForTest(stale, 10_000, 8, false).status)
	require.Less(t, time.Since(start), schedulerSlack,
		"an unknown set can never deliver, so the wait must not block on it")
}

// TestSystemEventSet_MaskFiltering covers the registration mask reaching the
// engine through the request struct.
func TestSystemEventSet_MaskFiltering(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))
	token := newSystemEventSet(t, engine.SystemEventTypeGpuDriverBind)

	b.fail(t, "lost")
	require.Equal(t, uint32(nvml.ERROR_TIMEOUT),
		systemEventWaitForTest(token, 100, 8, false).status,
		"a set registered for bind only must not receive the unbind")

	b.heal(t)
	got := systemEventWaitForTest(token, 5_000, 8, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Equal(t, engine.SystemEventTypeGpuDriverBind, got.events[0].Type)
}

func TestSystemEventSet_RejectsInvalidMask(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))
	token, status := systemEventSetCreateForTest()
	require.Equal(t, uint32(nvml.SUCCESS), status)
	t.Cleanup(func() { systemEventSetFreeForTest(token) })

	for name, mask := range map[string]uint64{
		"empty":       0,
		"unknown bit": 1 << 8,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT),
				systemRegisterEventsForTest(token, mask))
		})
	}
}

// TestSystemEventSet_FunctionNotFoundOnOlderDriver is the version gate. A
// consumer that probes the symbol on a driver that never had it must see
// FUNCTION_NOT_FOUND, the same answer real NVML's dlsym path produces.
func TestSystemEventSet_FunctionNotFoundOnOlderDriver(t *testing.T) {
	newBridgeEngine(t, profile(driverBeforeSystemEvents, 1, ""))

	token, status := systemEventSetCreateForTest()
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), status)
	require.Nil(t, token, "a rejected create must not hand back a set")

	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), systemEventSetFreeForTest(nil))
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND),
		systemRegisterEventsForTest(nil, bothSystemEventTypes))
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND),
		systemEventWaitForTest(nil, 0, 8, false).status)
}
