// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Accounting, MPS process lists, process detail and the sample buffers driven
// through the built library's C ABI.
//
// Most of this goes through go-nvml, which is how DCGM and the device plugin
// reach it. Three calls are driven from C instead. The process detail and
// process utilization getters take a caller-allocated array that go-nvml's
// wrappers never pass, so through go-nvml only the size probe is reachable; the
// C buffers here have a second element, which is what shows a wrong stride.
// The v2 accounting stats are driven from C to check the input pid and output
// fields independently of go-nvml.

package main

/*
#include <dlfcn.h>
#include <stdlib.h>

// The library's own definitions, so the arrays filled here are sized the way
// the library sizes them. The Dockerfile copies the header in beside the tests.
#include "nvml_types.h"

typedef struct nvmlDevice_st* acctDevice_t;

#define ACCT_SYMBOL_MISSING 0xFFFFFFFFu

static void* acctSym(const char* name) {
    void* lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_GLOBAL);
    if (lib == NULL) {
        return NULL;
    }
    return dlsym(lib, name);
}

static unsigned int acctDeviceByIndex(unsigned int index, acctDevice_t* device) {
    unsigned int (*fn)(unsigned int, acctDevice_t*) =
        (unsigned int (*)(unsigned int, acctDevice_t*))acctSym("nvmlDeviceGetHandleByIndex_v2");
    if (fn == NULL) {
        return ACCT_SYMBOL_MISSING;
    }
    return fn(index, device);
}

static unsigned int acctProcessDetailList(unsigned int index, nvmlProcessDetailList_v1_t* plist) {
    unsigned int (*fn)(acctDevice_t, nvmlProcessDetailList_v1_t*) =
        (unsigned int (*)(acctDevice_t, nvmlProcessDetailList_v1_t*))acctSym("nvmlDeviceGetRunningProcessDetailList");
    acctDevice_t device;
    unsigned int ret;
    if (fn == NULL) {
        return ACCT_SYMBOL_MISSING;
    }
    if ((ret = acctDeviceByIndex(index, &device)) != 0) {
        return ret;
    }
    return fn(device, plist);
}

static unsigned int acctProcessesUtilization(unsigned int index, nvmlProcessesUtilizationInfo_v1_t* info) {
    unsigned int (*fn)(acctDevice_t, nvmlProcessesUtilizationInfo_v1_t*) =
        (unsigned int (*)(acctDevice_t, nvmlProcessesUtilizationInfo_v1_t*))acctSym("nvmlDeviceGetProcessesUtilizationInfo");
    acctDevice_t device;
    unsigned int ret;
    if (fn == NULL) {
        return ACCT_SYMBOL_MISSING;
    }
    if ((ret = acctDeviceByIndex(index, &device)) != 0) {
        return ret;
    }
    return fn(device, info);
}

static unsigned int acctAccountingStatsV2(unsigned int index, nvmlAccountingStats_v2_t* stats) {
    unsigned int (*fn)(acctDevice_t, nvmlAccountingStats_v2_t*) =
        (unsigned int (*)(acctDevice_t, nvmlAccountingStats_v2_t*))acctSym("nvmlDeviceGetAccountingStats_v2");
    acctDevice_t device;
    unsigned int ret;
    if (fn == NULL) {
        return ACCT_SYMBOL_MISSING;
    }
    if ((ret = acctDeviceByIndex(index, &device)) != 0) {
        return ret;
    }
    return fn(device, stats);
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Fixture values from util-test-config.yaml's device 5, the one board with
// accounting on and an MPS pair. Device 0 keeps the defaults: accounting off,
// 72 W, JPEG at 35%.
const (
	acctDevice          = 5
	acctDisabledDevice  = 0
	acctServerPID       = 4300
	acctClientPID       = 4301
	acctClientMiB       = 4096
	acctClientSmUtil    = 60
	acctClientMemUtil   = 20
	acctTerminatedPID   = 3131
	acctTerminatedGpu   = 62
	acctTerminatedMem   = 31
	acctTerminatedMiB   = 1024
	acctTerminatedMS    = 120000
	acctUnknownPID      = 9999
	samplesPowerMW      = 72000
	samplesJpegPercent  = 35
	samplesBufferLength = 20
	oneMiB              = 1024 * 1024
)

func testAccountingAndProcesses(deviceCount int) []testResult {
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		return []testResult{skippedResult("accounting",
			"needs MOCK_NVML_CONFIG for the fixture's accounting buffer and MPS pair")}
	}
	if deviceCount <= acctDevice {
		return []testResult{{"accounting", false,
			fmt.Sprintf("fixture enumerates %d devices; device %d carries the accounting state", deviceCount, acctDevice)}}
	}
	device, ret := nvml.DeviceGetHandleByIndex(acctDevice)
	if ret != nvml.SUCCESS {
		return []testResult{{"accounting", false, fmt.Sprintf("GetHandleByIndex(%d): %v", acctDevice, nvml.ErrorString(ret))}}
	}

	results := testMPSProcessLists(device)
	results = append(results, checkProcessDetailListFromC()...)
	results = append(results, checkProcessesUtilizationFromC()...)
	results = append(results, testSamples()...)
	// Last, because it changes the device's accounting state.
	return append(results, testAccounting(device)...)
}

// testMPSProcessLists pins the split NVML draws under MPS: the server in the
// compute list, the client in the MPS list, neither in the other.
func testMPSProcessLists(device nvml.Device) []testResult {
	var results []testResult

	mps, ret := device.GetMPSComputeRunningProcesses()
	switch {
	case ret != nvml.SUCCESS:
		results = append(results, testResult{"processes/mps", false,
			fmt.Sprintf("GetMPSComputeRunningProcesses: %v", nvml.ErrorString(ret))})
	case len(mps) != 1 || mps[0].Pid != acctClientPID || mps[0].UsedGpuMemory != acctClientMiB*oneMiB:
		results = append(results, testResult{"processes/mps", false,
			fmt.Sprintf("got %+v; want only pid %d using %d MiB", mps, acctClientPID, acctClientMiB)})
	default:
		results = append(results, testResult{"processes/mps", true, ""})
	}

	compute, ret := device.GetComputeRunningProcesses()
	switch {
	case ret != nvml.SUCCESS:
		results = append(results, testResult{"processes/compute_excludes_mps_client", false,
			fmt.Sprintf("GetComputeRunningProcesses: %v", nvml.ErrorString(ret))})
	case len(compute) != 1 || compute[0].Pid != acctServerPID:
		results = append(results, testResult{"processes/compute_excludes_mps_client", false,
			fmt.Sprintf("got %+v; want only the MPS server, pid %d", compute, acctServerPID)})
	default:
		results = append(results, testResult{"processes/compute_excludes_mps_client", true, ""})
	}

	return results
}

// structV1 is NVML_STRUCT_VERSION(…, 1) for a struct of the given size.
func structV1(size uintptr) C.uint {
	return C.uint(uint32(size) | 1<<24)
}

// acctResult records a check, carrying the formatted detail only on failure.
func acctResult(label string, ok bool, format string, args ...any) testResult {
	if ok {
		return testResult{label, true, ""}
	}
	return testResult{label, false, fmt.Sprintf(format, args...)}
}

// checkProcessDetailListFromC covers the probe, the fill and the version
// refusal. The fill asks for every process, which puts the server and the
// client in one array: the client in the second slot is what a wrong element
// stride would misplace.
func checkProcessDetailListFromC() []testResult {
	const capacity = 4
	version := structV1(unsafe.Sizeof(C.nvmlProcessDetailList_v1_t{}))
	var results []testResult

	probe := C.nvmlProcessDetailList_v1_t{version: version, mode: C.NVML_PROCESS_MODE_ALL}
	ret := C.acctProcessDetailList(acctDevice, &probe)
	results = append(results, acctResult("processes/detail_list_probe_from_c",
		ret == C.NVML_ERROR_INSUFFICIENT_SIZE && probe.numProcArrayEntries == 2,
		"probe -> %d with %d entries; want INSUFFICIENT_SIZE with 2", ret, probe.numProcArrayEntries))

	unversioned := C.nvmlProcessDetailList_v1_t{mode: C.NVML_PROCESS_MODE_ALL}
	ret = C.acctProcessDetailList(acctDevice, &unversioned)
	results = append(results, acctResult("processes/detail_list_unversioned_from_c",
		ret == C.NVML_ERROR_INVALID_ARGUMENT,
		"unstamped version -> %d; want INVALID_ARGUMENT, as the header specifies for this call", ret))

	const label = "processes/detail_list_fill_from_c"
	array := (*C.nvmlProcessDetail_v1_t)(C.calloc(capacity, C.size_t(unsafe.Sizeof(C.nvmlProcessDetail_v1_t{}))))
	if array == nil {
		return append(results, testResult{label, false, "calloc failed"})
	}
	defer C.free(unsafe.Pointer(array))

	plist := C.nvmlProcessDetailList_v1_t{
		version:             version,
		mode:                C.NVML_PROCESS_MODE_ALL,
		numProcArrayEntries: capacity,
		procArray:           array,
	}
	if ret := C.acctProcessDetailList(acctDevice, &plist); ret != C.NVML_SUCCESS {
		return append(results, testResult{label, false, fmt.Sprintf("nvmlDeviceGetRunningProcessDetailList(mode=ALL) -> %d", ret)})
	}
	entries := unsafe.Slice(array, int(plist.numProcArrayEntries))
	if len(entries) != 2 || entries[0].pid != acctServerPID || entries[1].pid != acctClientPID ||
		entries[1].usedGpuMemory != acctClientMiB*oneMiB {
		return append(results, testResult{label, false, fmt.Sprintf("got %+v; want pids %d then %d, the second using %d MiB",
			entries, acctServerPID, acctClientPID, acctClientMiB)})
	}
	return append(results, testResult{label, true, ""})
}

// checkProcessesUtilizationFromC covers the probe, the fill and the version
// refusal. go-nvml reaches only the probe, as it never passes an array.
func checkProcessesUtilizationFromC() []testResult {
	const capacity = 4
	version := structV1(unsafe.Sizeof(C.nvmlProcessesUtilizationInfo_v1_t{}))
	var results []testResult

	probe := C.nvmlProcessesUtilizationInfo_v1_t{version: version}
	ret := C.acctProcessesUtilization(acctDevice, &probe)
	results = append(results, acctResult("processes/utilization_info_probe_from_c",
		ret == C.NVML_ERROR_INSUFFICIENT_SIZE && probe.processSamplesCount == 2,
		"probe -> %d with count %d; want INSUFFICIENT_SIZE with 2", ret, probe.processSamplesCount))

	unversioned := C.nvmlProcessesUtilizationInfo_v1_t{}
	ret = C.acctProcessesUtilization(acctDevice, &unversioned)
	results = append(results, acctResult("processes/utilization_info_unversioned_from_c",
		ret == C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH,
		"unstamped version -> %d; want ARGUMENT_VERSION_MISMATCH", ret))

	const label = "processes/utilization_info_fill_from_c"
	array := (*C.nvmlProcessUtilizationInfo_v1_t)(C.calloc(capacity, C.size_t(unsafe.Sizeof(C.nvmlProcessUtilizationInfo_v1_t{}))))
	if array == nil {
		return append(results, testResult{label, false, "calloc failed"})
	}
	defer C.free(unsafe.Pointer(array))

	info := C.nvmlProcessesUtilizationInfo_v1_t{
		version:             version,
		processSamplesCount: capacity,
		procUtilArray:       array,
	}
	if ret := C.acctProcessesUtilization(acctDevice, &info); ret != C.NVML_SUCCESS {
		return append(results, testResult{label, false, fmt.Sprintf("nvmlDeviceGetProcessesUtilizationInfo -> %d", ret)})
	}
	entries := unsafe.Slice(array, int(info.processSamplesCount))
	if len(entries) != 2 || entries[1].pid != acctClientPID ||
		entries[1].smUtil != acctClientSmUtil || entries[1].memUtil != acctClientMemUtil || entries[1].timeStamp == 0 {
		return append(results, testResult{label, false, fmt.Sprintf("got %+v; want two entries, the second pid %d at sm %d%% mem %d%%",
			entries, acctClientPID, acctClientSmUtil, acctClientMemUtil)})
	}
	return append(results, testResult{label, true, ""})
}

// testSamples reads two sample types whose values differ from each other and
// from zero, so a value written to the wrong half of the union fails.
func testSamples() []testResult {
	device, ret := nvml.DeviceGetHandleByIndex(acctDisabledDevice)
	if ret != nvml.SUCCESS {
		return []testResult{{"samples", false, fmt.Sprintf("GetHandleByIndex(%d): %v", acctDisabledDevice, nvml.ErrorString(ret))}}
	}
	results := []testResult{
		checkSamples("samples/total_power", device, nvml.TOTAL_POWER_SAMPLES, samplesPowerMW),
		checkSamples("samples/jpeg_utilization", device, nvml.JPG_UTILIZATION_SAMPLES, samplesJpegPercent),
	}
	return append(results, checkSamplesSinceLastSeen("samples/last_seen", device))
}

func checkSamples(label string, device nvml.Device, samplingType nvml.SamplingType, want uint32) testResult {
	valueType, samples, ret := device.GetSamples(samplingType, 0)
	switch {
	case ret != nvml.SUCCESS:
		return testResult{label, false, fmt.Sprintf("GetSamples: %v", nvml.ErrorString(ret))}
	case valueType != nvml.VALUE_TYPE_UNSIGNED_INT:
		return testResult{label, false, fmt.Sprintf("value type %d; want unsigned int", valueType)}
	case len(samples) != samplesBufferLength:
		return testResult{label, false, fmt.Sprintf("%d samples; want the full buffer of %d", len(samples), samplesBufferLength)}
	}
	for i, s := range samples {
		if got := binary.NativeEndian.Uint32(s.SampleValue[:4]); got != want {
			return testResult{label, false, fmt.Sprintf("sample %d reads %d; want %d", i, got, want)}
		}
		if i > 0 && s.TimeStamp <= samples[i-1].TimeStamp {
			return testResult{label, false, "timestamps do not run oldest to newest"}
		}
	}
	return testResult{label, true, ""}
}

// checkSamplesSinceLastSeen hands back the newest timestamp, which is how a
// poller asks for only what is new. Nothing new is NOT_FOUND; a sample taken
// in between is the only other honest answer.
func checkSamplesSinceLastSeen(label string, device nvml.Device) testResult {
	_, all, ret := device.GetSamples(nvml.TOTAL_POWER_SAMPLES, 0)
	if ret != nvml.SUCCESS || len(all) == 0 {
		return testResult{label, false, fmt.Sprintf("GetSamples: %v", nvml.ErrorString(ret))}
	}
	newest := all[len(all)-1].TimeStamp
	_, after, ret := device.GetSamples(nvml.TOTAL_POWER_SAMPLES, newest)
	switch {
	case ret == nvml.ERROR_NOT_FOUND:
		return testResult{label, true, ""}
	case ret == nvml.SUCCESS && len(after) > 0 && after[0].TimeStamp > newest:
		return testResult{label, true, "a sample was taken between the two reads"}
	}
	return testResult{label, false, fmt.Sprintf("-> %v with %d samples; want NOT_FOUND or only newer samples",
		nvml.ErrorString(ret), len(after))}
}

// checkAccountingStatsV2FromC drives the v2 stats from C to check the struct
// layout independently of go-nvml. The struct carries its pid as an input, so
// a bridge reading the wrong word would answer about some other process or none.
//
// The symbol is newer than the r595 header, so on an older configured driver
// the right answer is that it does not exist; which branch runs is a property
// of the fixture's driver_version.
func checkAccountingStatsV2FromC() testResult {
	const label = "accounting/stats_v2_from_c"
	driver, ret := nvml.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return testResult{label, false, fmt.Sprintf("SystemGetDriverVersion: %v", nvml.ErrorString(ret))}
	}
	var major int
	if _, err := fmt.Sscanf(driver, "%d", &major); err != nil {
		return testResult{label, false, fmt.Sprintf("driver version %q: %v", driver, err)}
	}

	stats := C.nvmlAccountingStats_v2_t{pid: acctTerminatedPID}
	got := C.acctAccountingStatsV2(acctDevice, &stats)
	if major < 600 {
		return acctResult(label, got == C.NVML_ERROR_FUNCTION_NOT_FOUND,
			"on driver %s -> %d; want FUNCTION_NOT_FOUND, the call arrived after r595", driver, got)
	}
	wantSamples := C.uint(acctTerminatedMS / 1000)
	return acctResult(label, got == C.NVML_SUCCESS && stats.pid == acctTerminatedPID && stats.isRunning == 0 &&
		stats.sampleCount == wantSamples && stats.sumGpuUtil == C.ulonglong(acctTerminatedGpu*100)*C.ulonglong(wantSamples),
		"nvmlDeviceGetAccountingStats_v2(pid=%d) -> %+v, %d", acctTerminatedPID, stats, got)
}

// testAccounting reads the buffer, then drives the two setters and reads each
// write back: a setter that returned SUCCESS and recorded nothing is the
// failure the override document's cross-process persistence makes easy.
func testAccounting(device nvml.Device) []testResult {
	var results []testResult
	check := func(label string, ok bool, format string, args ...any) {
		results = append(results, acctResult("accounting/"+label, ok, format, args...))
	}

	disabled, ret := nvml.DeviceGetHandleByIndex(acctDisabledDevice)
	if ret == nvml.SUCCESS {
		_, ret = disabled.GetAccountingPids()
	}
	check("disabled_refuses", ret == nvml.ERROR_NOT_SUPPORTED,
		"GetAccountingPids on device %d -> %v; want NOT_SUPPORTED with accounting off",
		acctDisabledDevice, nvml.ErrorString(ret))

	mode, ret := device.GetAccountingMode()
	check("mode_enabled", ret == nvml.SUCCESS && mode == nvml.FEATURE_ENABLED,
		"GetAccountingMode -> %d, %v; want enabled", mode, nvml.ErrorString(ret))

	pids, ret := device.GetAccountingPids()
	wantAll := []int{acctTerminatedPID, acctServerPID, acctClientPID}
	check("pids", ret == nvml.SUCCESS && slices.Equal(pids, wantAll),
		"GetAccountingPids -> %v, %v; want %v: terminated, then running", pids, nvml.ErrorString(ret), wantAll)

	done, ret := device.GetAccountingStats(acctTerminatedPID)
	check("terminated_stats", ret == nvml.SUCCESS && done.IsRunning == 0 && done.Time == acctTerminatedMS &&
		done.GpuUtilization == acctTerminatedGpu && done.MemoryUtilization == acctTerminatedMem &&
		done.MaxMemoryUsage == acctTerminatedMiB*oneMiB,
		"GetAccountingStats(%d) -> %+v, %v", acctTerminatedPID, done, nvml.ErrorString(ret))

	running, ret := device.GetAccountingStats(acctClientPID)
	check("running_stats", ret == nvml.SUCCESS && running.IsRunning == 1 && running.Time == 0 &&
		running.GpuUtilization == acctClientSmUtil && running.MaxMemoryUsage == acctClientMiB*oneMiB,
		"GetAccountingStats(%d) -> %+v, %v; a running process reports no run time yet",
		acctClientPID, running, nvml.ErrorString(ret))

	results = append(results, checkAccountingStatsV2FromC())

	_, ret = device.GetAccountingStats(acctUnknownPID)
	check("unknown_pid", ret == nvml.ERROR_NOT_FOUND,
		"GetAccountingStats(%d) -> %v; want NOT_FOUND", acctUnknownPID, nvml.ErrorString(ret))

	wantRunning := []int{acctServerPID, acctClientPID}
	ret = device.ClearAccountingPids()
	pids, pidsRet := device.GetAccountingPids()
	check("clear_keeps_running", ret == nvml.SUCCESS && pidsRet == nvml.SUCCESS && slices.Equal(pids, wantRunning),
		"ClearAccountingPids -> %v, then pids %v, %v; want %v", nvml.ErrorString(ret), pids, nvml.ErrorString(pidsRet), wantRunning)

	ret = device.SetAccountingMode(nvml.FEATURE_DISABLED)
	mode, modeRet := device.GetAccountingMode()
	_, pidsRet = device.GetAccountingPids()
	check("disable", ret == nvml.SUCCESS && modeRet == nvml.SUCCESS && mode == nvml.FEATURE_DISABLED &&
		pidsRet == nvml.ERROR_NOT_SUPPORTED,
		"SetAccountingMode(disabled) -> %v, then mode %d and pids %v; want disabled and NOT_SUPPORTED",
		nvml.ErrorString(ret), mode, nvml.ErrorString(pidsRet))

	ret = device.SetAccountingMode(nvml.FEATURE_ENABLED)
	pids, pidsRet = device.GetAccountingPids()
	check("re_enable", ret == nvml.SUCCESS && pidsRet == nvml.SUCCESS && slices.Equal(pids, wantRunning),
		"SetAccountingMode(enabled) -> %v, then pids %v, %v; want %v",
		nvml.ErrorString(ret), pids, nvml.ErrorString(pidsRet), wantRunning)

	return results
}
