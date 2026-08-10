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
// With no event pending they block for the caller's timeout, as real NVML
// does: clients loop on the wait with no sleep of their own.

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

//export nvmlDeviceRegisterEvents
func nvmlDeviceRegisterEvents(device C.nvmlDevice_t, eventTypes C.ulonglong, set C.nvmlEventSet_t) C.nvmlReturn_t {
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSupportedEventTypes
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
	data.gpuInstanceId = 0
	data.computeInstanceId = 0
	return true
}

// waitForDelivery blocks until deliver reports an event or timeout elapses,
// re-checking every pollInterval. Free of C types so the blocking contract is
// unit-testable. Returning early would spin the caller's health loop.
func waitForDelivery(timeout, pollInterval time.Duration, deliver func() bool) bool {
	if deliver() {
		return true
	}
	// Zero timeout is a non-blocking poll.
	if timeout <= 0 {
		return false
	}
	// A non-positive interval would spin the loop below without ever sleeping.
	if pollInterval <= 0 {
		pollInterval = waitPollInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if remaining > pollInterval {
			remaining = pollInterval
		}
		time.Sleep(remaining)
		if deliver() {
			return true
		}
	}
}

// waitForXid mirrors real nvmlEventSetWait semantics: NVML_SUCCESS as soon as
// an injected Xid is pending, otherwise NVML_ERROR_TIMEOUT after blocking for
// up to timeoutms.
func waitForXid(set C.nvmlEventSet_t, data *C.nvmlEventData_t, timeoutms C.uint) C.nvmlReturn_t {
	// NVML rejects a NULL set or payload; fail fast instead of after the timeout.
	if set == nil || data == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	delivered := waitForDelivery(
		time.Duration(timeoutms)*time.Millisecond,
		waitPollInterval,
		func() bool { return pollPendingXid(data) },
	)
	if delivered {
		return C.NVML_SUCCESS
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
	status     uint32
	eventType  uint64
	eventData  uint64
	deviceSet  bool
	claimCount int
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
		return unsafe.Pointer(handle), xid, true
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
	out.deviceSet = unsafe.Pointer(data.device.handle) == unsafe.Pointer(handle)
	return out
}
