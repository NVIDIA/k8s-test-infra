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

// Package main provides NVML's system event set.
//
// This is the newer, node-scoped sibling of the per-device event set in
// events.go. Where that one reports Xid classes for one GPU, the system set
// carries only GPU driver bind / unbind transitions for the whole node, and it
// is the surface recent driver consumers and nv-sentinel-class agents subscribe
// to in order to learn that a GPU disappeared.
//
// The mock derives those transitions from failure injection: a GPU that trips
// into lost / fallen_off_bus is reported unbound, and one that returns to
// healthy is reported bound. Injecting through `nvml-mock-ctl fail --gpu N
// --mode lost` is therefore observable here without the consumer having to
// poll a getter and interpret ERROR_GPU_IS_LOST.
//
// Unlike nvmlEventSetWait, a lost GPU does not fail this wait: reporting the
// loss *is* the event, so aborting with ERROR_GPU_IS_LOST would swallow the
// only thing the caller asked for.
package main

/*
#include <stdlib.h>
#include "nvml_types.h"
*/
import "C"

import (
	"time"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlSystemEventSetCreate
func nvmlSystemEventSetCreate(request *C.nvmlSystemEventSetCreateRequest_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemEventSetCreate"); !ok {
		return ret
	}
	if request == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The token is the set's identity on both sides: C owns the allocation, as
	// it does for nvmlEventSetCreate, and the engine keys its queue by the
	// pointer value.
	token := C.malloc(1)
	if token == nil {
		return C.NVML_ERROR_MEMORY
	}
	if ret := engine.GetEngine().SystemEventSetCreate(token); ret != nvml.SUCCESS {
		C.free(token)
		return toReturn(ret)
	}
	request.set.handle = (*C.struct_nvmlSystemEventSet_st)(token)
	return C.NVML_SUCCESS
}

//export nvmlSystemEventSetFree
func nvmlSystemEventSetFree(request *C.nvmlSystemEventSetFreeRequest_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemEventSetFree"); !ok {
		return ret
	}
	if request == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	token := unsafe.Pointer(request.set.handle)
	// Drop the engine's queue before freeing the allocation: if the engine
	// rejects the token (double free) the memory is still owned by whoever
	// holds the live set.
	if ret := engine.GetEngine().SystemEventSetFree(token); ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	C.free(token)
	request.set.handle = nil
	return C.NVML_SUCCESS
}

//export nvmlSystemRegisterEvents
func nvmlSystemRegisterEvents(request *C.nvmlSystemRegisterEventRequest_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemRegisterEvents"); !ok {
		return ret
	}
	if request == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(engine.GetEngine().SystemRegisterEvents(
		unsafe.Pointer(request.set.handle),
		uint64(request.eventTypes),
	))
}

// systemEventPoller adapts the engine's non-blocking poll to the deliver
// callback waitForDelivery expects. A hard error (an unknown set) stops the
// wait immediately instead of blocking out the caller's timeout for a request
// that can never succeed.
type systemEventPoller struct {
	token  unsafe.Pointer
	max    int
	events []engine.SystemEvent
	status nvml.Return
}

func (p *systemEventPoller) poll() bool {
	events, ret := engine.GetEngine().SystemEventSetPoll(p.token, p.max)
	if ret != nvml.SUCCESS {
		p.status = ret
		return true
	}
	p.events = events
	return len(events) > 0
}

//export nvmlSystemEventSetWait
func nvmlSystemEventSetWait(request *C.nvmlSystemEventSetWaitRequest_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemEventSetWait"); !ok {
		return ret
	}
	if request == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if request.data == nil || request.dataSize == 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	token := unsafe.Pointer(request.set.handle)
	if token == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}

	request.numEvent = 0
	poller := &systemEventPoller{token: token, max: int(request.dataSize), status: nvml.SUCCESS}
	delivered := waitForDelivery(
		time.Duration(request.timeoutms)*time.Millisecond,
		waitPollInterval,
		poller.poll,
	)
	if poller.status != nvml.SUCCESS {
		return toReturn(poller.status)
	}
	if !delivered {
		return C.NVML_ERROR_TIMEOUT
	}

	out := unsafe.Slice(request.data, int(request.dataSize))
	for i, event := range poller.events {
		out[i].eventType = C.ulonglong(event.Type)
		out[i].gpuId = C.uint(event.GpuID)
	}
	request.numEvent = C.uint(len(poller.events))
	return C.NVML_SUCCESS
}

// Test files cannot name the C request structs (no cgo in _test.go), so these
// hooks build them the way go-nvml does and drive the real exports. The token
// is the set's whole identity, so a test can hold it between calls and let
// each hook rebuild the request around it.

func systemEventSetCreateForTest() (token unsafe.Pointer, status uint32) {
	var request C.nvmlSystemEventSetCreateRequest_t
	status = uint32(nvmlSystemEventSetCreate(&request))
	return unsafe.Pointer(request.set.handle), status
}

func systemEventSetFreeForTest(token unsafe.Pointer) uint32 {
	var request C.nvmlSystemEventSetFreeRequest_t
	request.set.handle = (*C.struct_nvmlSystemEventSet_st)(token)
	return uint32(nvmlSystemEventSetFree(&request))
}

func systemRegisterEventsForTest(token unsafe.Pointer, eventTypes uint64) uint32 {
	var request C.nvmlSystemRegisterEventRequest_t
	request.set.handle = (*C.struct_nvmlSystemEventSet_st)(token)
	request.eventTypes = C.ulonglong(eventTypes)
	return uint32(nvmlSystemRegisterEvents(&request))
}

// systemEventWaitResult is what the bridge wrote into the caller's event array.
type systemEventWaitResult struct {
	status uint32
	events []engine.SystemEvent
}

// systemEventWaitForTest allocates the caller-owned event array the way a real
// client does. dataSize 0 exercises the rejection path, as does nilData.
func systemEventWaitForTest(token unsafe.Pointer, timeoutms uint32, dataSize int, nilData bool) systemEventWaitResult {
	var request C.nvmlSystemEventSetWaitRequest_t
	request.set.handle = (*C.struct_nvmlSystemEventSet_st)(token)
	request.timeoutms = C.uint(timeoutms)
	request.dataSize = C.uint(dataSize)
	if !nilData && dataSize > 0 {
		entries := C.calloc(C.size_t(dataSize), C.size_t(unsafe.Sizeof(C.nvmlSystemEventData_v1_t{})))
		if entries == nil {
			return systemEventWaitResult{status: C.NVML_ERROR_MEMORY}
		}
		defer C.free(entries)
		request.data = (*C.nvmlSystemEventData_v1_t)(entries)
	}

	out := systemEventWaitResult{status: uint32(nvmlSystemEventSetWait(&request))}
	if request.data == nil {
		return out
	}
	written := unsafe.Slice(request.data, dataSize)
	for i := 0; i < int(request.numEvent) && i < dataSize; i++ {
		out.events = append(out.events, engine.SystemEvent{
			Type:  uint64(written[i].eventType),
			GpuID: uint32(written[i].gpuId),
		})
	}
	return out
}
