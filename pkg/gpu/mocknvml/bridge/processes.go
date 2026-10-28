// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package main provides the NVML running-process lists and the sample
// buffers. The per-type lists all answer one count-probe-then-fill contract,
// implemented once in runningProcessesExport.
//
// nvidia-smi lists processes through the internal export table (internal.go),
// not these, so tests/mocknvml is where they are covered.
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

// Version tags a caller must stamp before these two getters read the struct's
// inputs; go-nvml's own bindings stamp them.
func processDetailListVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlProcessDetailList_v1_t{}), 1)
}

func processesUtilizationInfoVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlProcessesUtilizationInfo_v1_t{}), 1)
}

func lookupProcessDevice(device C.nvmlDevice_t) *engine.ConfigurableDevice {
	return engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
}

// runningProcessesExport answers a running-process list query. Its _v2 and _v3
// revisions share one layout, so every revision past _v1 lands here.
func runningProcessesExport(
	funcName string,
	device C.nvmlDevice_t,
	infoCount *C.uint,
	infos *C.nvmlProcessInfo_t,
	list func(*engine.ConfigurableDevice) ([]nvml.ProcessInfo, nvml.Return),
) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck(funcName); !ok {
		return ret
	}
	if infoCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupProcessDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	procs, ret := list(dev)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// Count probe. NVML's form for it is *infoCount = 0 with infos allowed to be
	// NULL, and the return code — not the count — is what tells the caller
	// whether to allocate and call again: INSUFFICIENT_SIZE means "there are
	// processes", SUCCESS means "there are none". Answering SUCCESS either way
	// reads as an idle GPU to a caller written to the header, which then never
	// makes the filling call.
	if infos == nil || len(procs) > int(*infoCount) {
		*infoCount = C.uint(len(procs))
		if len(procs) == 0 {
			return C.NVML_SUCCESS
		}
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	out := unsafe.Slice(infos, len(procs))
	for i, p := range procs {
		out[i] = C.nvmlProcessInfo_t{
			pid:               C.uint(p.Pid),
			usedGpuMemory:     C.ulonglong(p.UsedGpuMemory),
			gpuInstanceId:     C.uint(p.GpuInstanceId),
			computeInstanceId: C.uint(p.ComputeInstanceId),
		}
	}
	*infoCount = C.uint(len(procs))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetComputeRunningProcesses_v2
func nvmlDeviceGetComputeRunningProcesses_v2(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_v2_t) C.nvmlReturn_t {
	return runningProcessesExport("nvmlDeviceGetComputeRunningProcesses_v2", device, infoCount, infos,
		(*engine.ConfigurableDevice).GetComputeRunningProcesses)
}

//export nvmlDeviceGetGraphicsRunningProcesses_v2
func nvmlDeviceGetGraphicsRunningProcesses_v2(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_v2_t) C.nvmlReturn_t {
	return runningProcessesExport("nvmlDeviceGetGraphicsRunningProcesses_v2", device, infoCount, infos,
		(*engine.ConfigurableDevice).GetGraphicsRunningProcesses)
}

//export nvmlDeviceGetMPSComputeRunningProcesses_v2
func nvmlDeviceGetMPSComputeRunningProcesses_v2(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_v2_t) C.nvmlReturn_t {
	return runningProcessesExport("nvmlDeviceGetMPSComputeRunningProcesses_v2", device, infoCount, infos,
		(*engine.ConfigurableDevice).GetMPSComputeRunningProcesses)
}

//export nvmlDeviceGetMPSComputeRunningProcesses_v3
func nvmlDeviceGetMPSComputeRunningProcesses_v3(device C.nvmlDevice_t, infoCount *C.uint, infos *C.nvmlProcessInfo_t) C.nvmlReturn_t {
	return runningProcessesExport("nvmlDeviceGetMPSComputeRunningProcesses_v3", device, infoCount, infos,
		(*engine.ConfigurableDevice).GetMPSComputeRunningProcesses)
}

//export nvmlDeviceGetRunningProcessDetailList
func nvmlDeviceGetRunningProcessDetailList(device C.nvmlDevice_t, plist *C.nvmlProcessDetailList_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetRunningProcessDetailList"); !ok {
		return ret
	}
	// The header answers a bad version with INVALID_ARGUMENT here, not the
	// ARGUMENT_VERSION_MISMATCH most versioned getters use.
	if plist == nil || uint32(plist.version) != processDetailListVersion() {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupProcessDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	procs, ret := dev.RunningProcessesByMode(nvml.ProcessMode(plist.mode))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// Same count-probe contract as runningProcessesExport.
	if plist.procArray == nil || len(procs) > int(plist.numProcArrayEntries) {
		plist.numProcArrayEntries = C.uint(len(procs))
		if len(procs) == 0 {
			return C.NVML_SUCCESS
		}
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	out := unsafe.Slice(plist.procArray, len(procs))
	for i, p := range procs {
		out[i] = C.nvmlProcessDetail_v1_t{
			pid:               C.uint(p.Pid),
			usedGpuMemory:     C.ulonglong(p.UsedGpuMemory),
			gpuInstanceId:     C.uint(p.GpuInstanceId),
			computeInstanceId: C.uint(p.ComputeInstanceId),
		}
	}
	plist.numProcArrayEntries = C.uint(len(procs))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetProcessesUtilizationInfo
func nvmlDeviceGetProcessesUtilizationInfo(device C.nvmlDevice_t, procesesUtilInfo *C.nvmlProcessesUtilizationInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetProcessesUtilizationInfo"); !ok {
		return ret
	}
	info := procesesUtilInfo
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(info.version) != processesUtilizationInfoVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupProcessDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	samples, ret := dev.GetProcessUtilization(uint64(info.lastSeenTimeStamp))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// Like nvmlDeviceGetProcessUtilization, nothing to report is NOT_FOUND
	// rather than an empty success, on the probe and the fill alike.
	if len(samples) == 0 {
		info.processSamplesCount = 0
		return C.NVML_ERROR_NOT_FOUND
	}
	if info.procUtilArray == nil || len(samples) > int(info.processSamplesCount) {
		info.processSamplesCount = C.uint(len(samples))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	out := unsafe.Slice(info.procUtilArray, len(samples))
	for i, s := range samples {
		// Processes carry no JPEG or OFA share, so those stay zero.
		out[i] = C.nvmlProcessUtilizationInfo_v1_t{
			timeStamp: C.ulonglong(s.TimeStamp),
			pid:       C.uint(s.Pid),
			smUtil:    C.uint(s.SmUtil),
			memUtil:   C.uint(s.MemUtil),
			encUtil:   C.uint(s.EncUtil),
			decUtil:   C.uint(s.DecUtil),
		}
	}
	info.processSamplesCount = C.uint(len(samples))
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSamples
func nvmlDeviceGetSamples(device C.nvmlDevice_t, _type C.nvmlSamplingType_t, lastSeenTimeStamp C.ulonglong, sampleValType *C.nvmlValueType_t, sampleCount *C.uint, samples *C.nvmlSample_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetSamples"); !ok {
		return ret
	}
	if sampleValType == nil || sampleCount == nil || (samples != nil && *sampleCount == 0) {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupProcessDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	valueType, list, ret := dev.GetSamples(nvml.SamplingType(_type), uint64(lastSeenTimeStamp))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*sampleValType = C.nvmlValueType_t(valueType)
	// Unlike the process lists, the probe succeeds: go-nvml allocates only
	// after a SUCCESS that carries the count.
	if samples == nil {
		*sampleCount = C.uint(len(list))
		return C.NVML_SUCCESS
	}
	*sampleCount = C.uint(writeSamples(list, unsafe.Slice(samples, int(*sampleCount))))
	return C.NVML_SUCCESS
}

// writeSamples copies as many samples as out holds and returns how many. A
// short buffer is filled rather than refused, and with the newest samples,
// which are the ones a poller is after.
func writeSamples(list []nvml.Sample, out []C.nvmlSample_t) int {
	if len(out) < len(list) {
		list = list[len(list)-len(out):]
	}
	for i, s := range list {
		out[i].timeStamp = C.ulonglong(s.TimeStamp)
		*(*[8]byte)(unsafe.Pointer(&out[i].sampleValue)) = s.SampleValue
	}
	return len(list)
}
