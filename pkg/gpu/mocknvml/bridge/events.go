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

// Package main provides NVML event set implementations.
//
// nvidia-smi calls nvmlEventSetCreate during initialization and fails
// if it returns an error, so the create/free/register stubs must always
// succeed. nvmlEventSetWait_v1/_v2 are wired through to the engine's failure
// injector: when a device trips with a `failure.xid:` block configured,
// the next wait call returns NVML_SUCCESS with the configured Xid in
// the standard nvmlEventData_t structure (NVML_EVENT_TYPE_XID_CRITICAL_ERROR).
// Real NVML clients (dcgm-exporter, the device plugin's health monitor)
// see the Xid through the API designed for it instead of having to
// scrape it out of an overloaded ViolationTime field.
//
// After a lost / fallen_off_bus device has tripped, and after any
// configured Xid has been delivered once, subsequent waits return
// NVML_ERROR_GPU_IS_LOST immediately — matching real NVML after Xid 79,
// and without blocking for the timeout. Clients (the DRA driver,
// dcgm-exporter) already back off on that error. A wait never trips the
// injector; it only observes a device that some other guarded call has
// already lost.
//
// With no event pending and no lost device they block for the caller's
// timeout, as real NVML does: clients loop on the wait with no sleep of
// their own.
package main

/*
#include <stdlib.h>
#include "nvml_types.h"
*/
import "C"

import (
	"time"
	"unsafe"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// waitPollInterval bounds how long a blocking wait sits before re-checking for
// an injected Xid, trading delivery latency against wakeups.
const waitPollInterval = 100 * time.Millisecond

//export nvmlEventSetCreate
func nvmlEventSetCreate(set *C.nvmlEventSet_t) C.nvmlReturn_t {
	if set == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	p := C.malloc(1)
	if p == nil {
		return C.NVML_ERROR_MEMORY
	}
	*set = C.nvmlEventSet_t(p)
	return C.NVML_SUCCESS
}

//export nvmlEventSetFree
func nvmlEventSetFree(set C.nvmlEventSet_t) C.nvmlReturn_t {
	if set != nil {
		C.free(unsafe.Pointer(set))
	}
	return C.NVML_SUCCESS
}

//
//export nvmlDeviceRegisterEvents
//nolint:revive // cgo //export ABI: params keep their NVML names for the generated C header
func nvmlDeviceRegisterEvents(device C.nvmlDevice_t, eventTypes C.ulonglong, set C.nvmlEventSet_t) C.nvmlReturn_t {
	return C.NVML_SUCCESS
}

//
//export nvmlDeviceGetSupportedEventTypes
//nolint:revive // cgo //export ABI: params keep their NVML names for the generated C header
func nvmlDeviceGetSupportedEventTypes(device C.nvmlDevice_t, eventTypes *C.ulonglong) C.nvmlReturn_t {
	if eventTypes == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Advertise XID_CRITICAL_ERROR as supported so consumers (dcgm-exporter,
	// device-plugin health monitor) actually register for the event class
	// the failure injector emits. Returning 0 here would cause well-behaved
	// callers to skip RegisterEvents entirely and never observe the Xid we
	// surface from nvmlEventSetWait_v2 below.
	*eventTypes = C.NVML_EVENT_TYPE_XID_CRITICAL_ERROR
	return C.NVML_SUCCESS
}

// pendingXidClaim is the engine call pollPendingXid consults. A variable only
// so tests can drive delivery without the engine singleton.
var pendingXidClaim = func() (unsafe.Pointer, uint64, bool) {
	return engine.GetEngine().PendingXidEvent()
}

// anyDeviceLost is the engine call waitForXid consults to fail a wait with
// NVML_ERROR_GPU_IS_LOST. A variable only so tests can drive it without
// the engine singleton.
var anyDeviceLost = func() bool {
	return engine.GetEngine().AnyDeviceLost()
}

// pollPendingXid asks the engine for the next undelivered Xid critical
// error (produced by failure injection) and, if one exists, populates
// `data` with the standard NVML event payload. Returns true when an
// event was delivered.
func pollPendingXid(data *C.nvmlEventData_t) bool {
	if data == nil {
		return false
	}
	handle, xid, ok := pendingXidClaim()
	if !ok {
		return false
	}
	// The engine returns the C block it allocated for this device, so this
	// is the same pointer-to-pointer conversion device.go's handle lookup
	// helpers do.
	data.device.handle = (*C.struct_nvmlDevice_st)(handle)
	data.eventType = C.NVML_EVENT_TYPE_XID_CRITICAL_ERROR
	data.eventData = C.ulonglong(xid)
	// Real NVML reports 0xFFFFFFFF for both instance ids on events that are
	// not scoped to a MIG instance. Consumers such as the DRA driver and the
	// device plugin key off that sentinel to attribute the Xid to the whole
	// GPU; (0, 0) would name MIG GI 0 / CI 0 instead and be dropped on a GPU
	// without MIG enabled.
	data.gpuInstanceId = C.uint(nvmlNoMIGInstanceID)
	data.computeInstanceId = C.uint(nvmlNoMIGInstanceID)
	return true
}

// waitForDelivery blocks until deliver reports an event or timeout elapses,
// re-checking every pollInterval. Free of C types so the blocking contract is
// unit-testable. Returning early would spin the caller's health loop.
func waitForDelivery(timeout, pollInterval time.Duration, deliver func() bool) bool {
	delivered, _ := waitForDeliveryOrAbort(timeout, pollInterval, deliver, nil)
	return delivered
}

// waitForDeliveryOrAbort is waitForDelivery plus an abort check (lost GPU)
// after every poll. abort==true returns immediately without waiting out the
// timeout, matching real nvmlEventSetWait after a device is lost. A pending
// event still wins: deliver is checked first so a configured Xid 79 is
// handed to the caller before ERROR_GPU_IS_LOST starts.
func waitForDeliveryOrAbort(timeout, pollInterval time.Duration, deliver, abort func() bool) (delivered, aborted bool) {
	if delivered, aborted = pollDeliverOrAbort(deliver, abort); delivered || aborted {
		return delivered, aborted
	}
	// Zero timeout is a non-blocking poll.
	if timeout <= 0 {
		return false, false
	}
	return pollUntil(timeout, clampPollInterval(pollInterval), deliver, abort)
}

func clampPollInterval(pollInterval time.Duration) time.Duration {
	// A non-positive interval would spin the loop below without ever sleeping.
	if pollInterval <= 0 {
		return waitPollInterval
	}
	return pollInterval
}

// pollUntil sleeps until deliver or abort fires, or timeout elapses.
func pollUntil(timeout, pollInterval time.Duration, deliver, abort func() bool) (delivered, aborted bool) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// A Xid that lands after the deadline is a TIMEOUT. A lost GPU
			// observed at the same instant is ERROR_GPU_IS_LOST: real NVML
			// does not wait out the timeout for that error.
			return false, abort != nil && abort()
		}
		if remaining > pollInterval {
			remaining = pollInterval
		}
		time.Sleep(remaining)
		if delivered, aborted = pollDeliverOrAbort(deliver, abort); delivered || aborted {
			return delivered, aborted
		}
	}
}

func pollDeliverOrAbort(deliver, abort func() bool) (delivered, aborted bool) {
	if deliver != nil && deliver() {
		return true, false
	}
	if abort != nil && abort() {
		return false, true
	}
	return false, false
}

// waitForXid mirrors real nvmlEventSetWait semantics: NVML_SUCCESS as soon as
// an injected Xid is pending, NVML_ERROR_GPU_IS_LOST immediately if a device
// has already tripped into lost / fallen_off_bus (and no Xid remains),
// otherwise NVML_ERROR_TIMEOUT after blocking for up to timeoutms.
func waitForXid(set C.nvmlEventSet_t, data *C.nvmlEventData_t, timeoutms C.uint) C.nvmlReturn_t {
	// NVML rejects a NULL set or payload; fail fast instead of after the timeout.
	if set == nil || data == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	delivered, aborted := waitForDeliveryOrAbort(
		time.Duration(timeoutms)*time.Millisecond,
		waitPollInterval,
		func() bool { return pollPendingXid(data) },
		anyDeviceLost,
	)
	if delivered {
		return C.NVML_SUCCESS
	}
	if aborted {
		return C.NVML_ERROR_GPU_IS_LOST
	}
	return C.NVML_ERROR_TIMEOUT
}

//export nvmlEventSetWait_v1
func nvmlEventSetWait_v1(set C.nvmlEventSet_t, data *C.nvmlEventData_t, timeoutms C.uint) C.nvmlReturn_t {
	return waitForXid(set, data, timeoutms)
}

//export nvmlEventSetWait_v2
func nvmlEventSetWait_v2(set C.nvmlEventSet_t, data *C.nvmlEventData_t, timeoutms C.uint) C.nvmlReturn_t {
	return waitForXid(set, data, timeoutms)
}

// eventSetWaitTestArgs selects which of the wait's pointer arguments are NULL,
// so tests can cover the rejection paths without naming C types.
type eventSetWaitTestArgs struct {
	nilSet  bool
	nilData bool
}

// Test files cannot construct an nvmlEventSet_t or an nvmlEventData_t (no cgo
// in _test.go), so these hooks drive the exported entry points, allocating both
// the way a real caller does unless args asks for a NULL.
func eventSetWaitV1ForTest(args eventSetWaitTestArgs, timeoutms uint32) uint32 {
	return eventSetWaitForTest(nvmlEventSetWait_v1, args, timeoutms)
}

func eventSetWaitV2ForTest(args eventSetWaitTestArgs, timeoutms uint32) uint32 {
	return eventSetWaitForTest(nvmlEventSetWait_v2, args, timeoutms)
}

func eventSetWaitForTest(
	wait func(C.nvmlEventSet_t, *C.nvmlEventData_t, C.uint) C.nvmlReturn_t,
	args eventSetWaitTestArgs,
	timeoutms uint32,
) uint32 {
	var set C.nvmlEventSet_t
	if !args.nilSet {
		if ret := nvmlEventSetCreate(&set); ret != C.NVML_SUCCESS {
			return uint32(ret)
		}
		defer nvmlEventSetFree(set)
	}
	var data C.nvmlEventData_t
	payload := &data
	if args.nilData {
		payload = nil
	}
	return uint32(wait(set, payload, C.uint(timeoutms)))
}

// xidDelivery is what the bridge wrote into the caller's nvmlEventData_t.
type xidDelivery struct {
	status            uint32
	eventType         uint64
	eventData         uint64
	gpuInstanceID     uint32
	computeInstanceID uint32
	deviceSet         bool
	claimCount        int
}

// eventSetWaitWithPendingXidForTest stubs the engine claim with one pending Xid
// and drives the exported wait, covering the delivery branch and its payload.
// availableAfter delays the claim, for the already-parked case.
func eventSetWaitWithPendingXidForTest(xid uint64, timeoutms uint32, availableAfter time.Duration) xidDelivery {
	// Stands in for the engine's per-device C block; the bridge only copies the
	// pointer through, so any valid allocation proves the wiring.
	handle := C.malloc(1)
	defer C.free(handle)

	var out xidDelivery
	available := time.Now().Add(availableAfter)
	claimed := false

	restore := pendingXidClaim
	pendingXidClaim = func() (unsafe.Pointer, uint64, bool) {
		out.claimCount++
		if claimed || time.Now().Before(available) {
			return nil, 0, false
		}
		claimed = true
		return handle, xid, true
	}
	defer func() { pendingXidClaim = restore }()

	var set C.nvmlEventSet_t
	if ret := nvmlEventSetCreate(&set); ret != C.NVML_SUCCESS {
		out.status = uint32(ret)
		return out
	}
	defer nvmlEventSetFree(set)

	var data C.nvmlEventData_t
	out.status = uint32(nvmlEventSetWait_v2(set, &data, C.uint(timeoutms)))
	out.eventType = uint64(data.eventType)
	out.eventData = uint64(data.eventData)
	out.gpuInstanceID = uint32(data.gpuInstanceId)
	out.computeInstanceID = uint32(data.computeInstanceId)
	out.deviceSet = unsafe.Pointer(data.device.handle) == handle
	return out
}

// eventSetWaitWhenLostForTest stubs the engine so no Xid is pending and
// anyDeviceLost becomes true after lostAfter, then drives the wait.
func eventSetWaitWhenLostForTest(
	wait func(eventSetWaitTestArgs, uint32) uint32,
	timeoutms uint32,
	lostAfter time.Duration,
) uint32 {
	restoreClaim := pendingXidClaim
	restoreLost := anyDeviceLost
	pendingXidClaim = func() (unsafe.Pointer, uint64, bool) {
		return nil, 0, false
	}
	available := time.Now().Add(lostAfter)
	anyDeviceLost = func() bool {
		return !time.Now().Before(available)
	}
	defer func() {
		pendingXidClaim = restoreClaim
		anyDeviceLost = restoreLost
	}()
	return wait(eventSetWaitTestArgs{}, timeoutms)
}

// eventSetWaitWithPendingXidAndLostForTest stubs a single pending Xid while
// the device is already lost, then drives two waits: the first must deliver
// the Xid, the second must return GPU_IS_LOST.
func eventSetWaitWithPendingXidAndLostForTest(xid uint64, timeoutms uint32) (first xidDelivery, second uint32) {
	handle := C.malloc(1)
	defer C.free(handle)

	claimed := false
	restoreClaim := pendingXidClaim
	restoreLost := anyDeviceLost
	pendingXidClaim = func() (unsafe.Pointer, uint64, bool) {
		first.claimCount++
		if claimed {
			return nil, 0, false
		}
		claimed = true
		return handle, xid, true
	}
	anyDeviceLost = func() bool { return true }
	defer func() {
		pendingXidClaim = restoreClaim
		anyDeviceLost = restoreLost
	}()

	var set C.nvmlEventSet_t
	if ret := nvmlEventSetCreate(&set); ret != C.NVML_SUCCESS {
		first.status = uint32(ret)
		return first, 0
	}
	defer nvmlEventSetFree(set)

	var data C.nvmlEventData_t
	first.status = uint32(nvmlEventSetWait_v2(set, &data, C.uint(timeoutms)))
	first.eventType = uint64(data.eventType)
	first.eventData = uint64(data.eventData)
	first.gpuInstanceID = uint32(data.gpuInstanceId)
	first.computeInstanceID = uint32(data.computeInstanceId)
	first.deviceSet = unsafe.Pointer(data.device.handle) == handle

	var data2 C.nvmlEventData_t
	second = uint32(nvmlEventSetWait_v2(set, &data2, C.uint(timeoutms)))
	return first, second
}
