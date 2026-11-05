// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML telemetry and diagnostic queries:
//   - nvmlDeviceGetClkMonStatus
//   - nvmlDeviceGetBBXTimeData_v1, nvmlDeviceGetLastBBXFlushTime
//   - nvmlDeviceReadPRMCounters_v1, nvmlDeviceReadWritePRM_v1
//
// nvmlDeviceClearFieldValues lives in fieldvalues.go, beside the accessors it
// shares with nvmlDeviceGetFieldValues. See issue NVIDIA/k8s-test-infra#1042.
package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlDeviceGetClkMonStatus
func nvmlDeviceGetClkMonStatus(device C.nvmlDevice_t, status *C.nvmlClkMonStatus_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetClkMonStatus"); !ok {
		return ret
	}
	if status == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetClkMonStatus()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// Zero the whole struct first: it arrives uninitialised, and the fault
	// list past clkMonListSize would otherwise carry caller garbage.
	*status = C.nvmlClkMonStatus_t{}
	status.bGlobalStatus = C.uint(got.BGlobalStatus)
	status.clkMonListSize = C.uint(got.ClkMonListSize)
	for i := range min(int(got.ClkMonListSize), len(status.clkMonList)) {
		status.clkMonList[i].clkApiDomain = C.uint(got.ClkMonList[i].ClkApiDomain)
		status.clkMonList[i].clkDomainFaultMask = C.uint(got.ClkMonList[i].ClkDomainFaultMask)
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetBBXTimeData_v1
func nvmlDeviceGetBBXTimeData_v1(device C.nvmlDevice_t, timeData *C.nvmlBBXTimeData_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetBBXTimeData_v1"); !ok {
		return ret
	}
	if timeData == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	got, ret := dev.GetBBXTimeData_v1()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	timeData.timeRun = C.uint(got.TimeRun)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetLastBBXFlushTime
func nvmlDeviceGetLastBBXFlushTime(device C.nvmlDevice_t, timestamp *C.ulonglong, durationUs *C.ulong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetLastBBXFlushTime"); !ok {
		return ret
	}
	if timestamp == nil || durationUs == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	ts, duration, ret := dev.GetLastBBXFlushTime()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*timestamp = C.ulonglong(ts)
	*durationUs = C.ulong(duration)
	return C.NVML_SUCCESS
}

//export nvmlDeviceReadPRMCounters_v1
func nvmlDeviceReadPRMCounters_v1(device C.nvmlDevice_t, counterList *C.nvmlPRMCounterList_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceReadPRMCounters_v1"); !ok {
		return ret
	}
	if counterList == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The engine declines without reading the request, so the opaque list is
	// neither decoded nor written.
	_, ret := dev.ReadPRMCounters_v1(nil, 0)
	return toReturn(ret)
}

//export nvmlDeviceReadWritePRM_v1
func nvmlDeviceReadWritePRM_v1(device C.nvmlDevice_t, buffer *C.nvmlPRMTLV_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceReadWritePRM_v1"); !ok {
		return ret
	}
	if buffer == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// As with ReadPRMCounters_v1, the opaque TLV buffer is never touched.
	return toReturn(dev.ReadWritePRM_v1(nil))
}
