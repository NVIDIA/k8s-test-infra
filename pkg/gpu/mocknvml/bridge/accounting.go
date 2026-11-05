// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML per-process accounting entry points. The
// records come from the engine; the mode getters sit with the other device
// getters in device.go.
//
// nvidia-smi reaches these through `--query-accounted-apps` and `-am`/`-caa`.
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

func lookupAccountingDevice(device C.nvmlDevice_t) *engine.ConfigurableDevice {
	return engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
}

//export nvmlDeviceGetAccountingPids
func nvmlDeviceGetAccountingPids(device C.nvmlDevice_t, count *C.uint, pids *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAccountingPids"); !ok {
		return ret
	}
	if count == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupAccountingDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	list, ret := dev.GetAccountingPids()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// The count probe is *count = 0 with pids NULL; go-nvml instead starts at a
	// one-entry buffer and doubles it on INSUFFICIENT_SIZE. Both read the
	// needed size back out of *count.
	if pids == nil || int(*count) < len(list) {
		*count = C.uint(len(list))
		if len(list) == 0 {
			return C.NVML_SUCCESS
		}
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	out := unsafe.Slice(pids, len(list))
	for i, pid := range list {
		out[i] = C.uint(pid)
	}
	*count = C.uint(len(list))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetAccountingStats
func nvmlDeviceGetAccountingStats(device C.nvmlDevice_t, pid C.uint, stats *C.nvmlAccountingStats_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAccountingStats"); !ok {
		return ret
	}
	if stats == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupAccountingDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	s, ret := dev.GetAccountingStats(uint32(pid))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*stats = C.nvmlAccountingStats_t{
		gpuUtilization:    C.uint(s.GpuUtilization),
		memoryUtilization: C.uint(s.MemoryUtilization),
		maxMemoryUsage:    C.ulonglong(s.MaxMemoryUsage),
		time:              C.ulonglong(s.Time),
		startTime:         C.ulonglong(s.StartTime),
		isRunning:         C.uint(s.IsRunning),
	}
	return C.NVML_SUCCESS
}

// nvmlDeviceGetAccountingStats_v2 takes the pid inside the struct rather than
// as an argument, so the one input is read back out before the struct is
// overwritten.
//
//export nvmlDeviceGetAccountingStats_v2
func nvmlDeviceGetAccountingStats_v2(device C.nvmlDevice_t, stats *C.nvmlAccountingStats_v2_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetAccountingStats_v2"); !ok {
		return ret
	}
	if stats == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupAccountingDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	s, ret := dev.GetAccountingStats_v2(uint32(stats.pid))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*stats = C.nvmlAccountingStats_v2_t{
		pid:               C.uint(s.Pid),
		isRunning:         C.uint(s.IsRunning),
		gpuUtilization:    C.uint(s.GpuUtilization),
		memoryUtilization: C.uint(s.MemoryUtilization),
		maxMemoryUsage:    C.ulonglong(s.MaxMemoryUsage),
		sampleCount:       C.uint(s.SampleCount),
		sumGpuUtil:        C.ulonglong(s.SumGpuUtil),
		sumFbUtil:         C.ulonglong(s.SumFbUtil),
		time:              C.ulonglong(s.Time),
		startTime:         C.ulonglong(s.StartTime),
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetAccountingMode
func nvmlDeviceSetAccountingMode(device C.nvmlDevice_t, mode C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetAccountingMode"); !ok {
		return ret
	}
	dev := lookupAccountingDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetAccountingMode(nvml.EnableState(mode)))
}

//export nvmlDeviceClearAccountingPids
func nvmlDeviceClearAccountingPids(device C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceClearAccountingPids"); !ok {
		return ret
	}
	dev := lookupAccountingDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.ClearAccountingPids())
}
