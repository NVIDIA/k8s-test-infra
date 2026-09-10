// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Array and buffer contract tests for the exports whose documented probe form
// go-nvml never uses. go-nvml reaches every one of these through a shape that
// happens to satisfy a stricter-than-documented bridge — it presets a count,
// or passes a NULL array, or allocates a buffer no realistic value overruns —
// so a bridge that answers the header's own recipe with an error still passes
// every Go consumer. Only a direct C call sees what the header asks for.

package main

/*
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// The library's own definitions, so the structs it fills by pointer here are
// sized the way it sizes them; a local copy that drifted by one field would
// let the library write past the end of these allocations, and a test that
// corrupts memory can pass. The Dockerfile copies the header in beside the
// tests.
#include "nvml_types.h"

// Handles stay an opaque pointer rather than the library's single-field
// wrapper struct: unwrapping it at every call site would say nothing these
// tests are about.
typedef struct nvmlDevice_st* contractDevice_t;

// Returned instead of an nvmlReturn_t when the symbol itself is missing, so a
// dropped export is distinguishable from a call that failed.
#define CONTRACT_SYMBOL_MISSING 0xFFFFFFFFu

// contractSym resolves a public NVML entry point. The library is already
// loaded through go-nvml, so this only fetches a handle to it.
static void* contractSym(const char* name) {
    void* lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_GLOBAL);
    if (lib == NULL) {
        return NULL;
    }
    return dlsym(lib, name);
}

static unsigned int contractDeviceHandleByIndex(unsigned int index, contractDevice_t* device) {
    unsigned int (*fn)(unsigned int, contractDevice_t*) =
        (unsigned int (*)(unsigned int, contractDevice_t*))contractSym("nvmlDeviceGetHandleByIndex_v2");
    if (fn == NULL) {
        return CONTRACT_SYMBOL_MISSING;
    }
    return fn(index, device);
}

static unsigned int contractRunningProcesses(const char* symbol, contractDevice_t device,
                                             unsigned int* infoCount, nvmlProcessInfo_t* infos) {
    unsigned int (*fn)(contractDevice_t, unsigned int*, nvmlProcessInfo_t*) =
        (unsigned int (*)(contractDevice_t, unsigned int*, nvmlProcessInfo_t*))contractSym(symbol);
    if (fn == NULL) {
        return CONTRACT_SYMBOL_MISSING;
    }
    return fn(device, infoCount, infos);
}

static unsigned int contractProcessName(unsigned int pid, char* name, unsigned int length) {
    unsigned int (*fn)(unsigned int, char*, unsigned int) =
        (unsigned int (*)(unsigned int, char*, unsigned int))contractSym("nvmlSystemGetProcessName");
    if (fn == NULL) {
        return CONTRACT_SYMBOL_MISSING;
    }
    return fn(pid, name, length);
}

static unsigned int contractProcessUtilization(contractDevice_t device,
                                               nvmlProcessUtilizationSample_t* utilization,
                                               unsigned int* count, unsigned long long lastSeen) {
    unsigned int (*fn)(contractDevice_t, nvmlProcessUtilizationSample_t*, unsigned int*, unsigned long long) =
        (unsigned int (*)(contractDevice_t, nvmlProcessUtilizationSample_t*, unsigned int*, unsigned long long))
            contractSym("nvmlDeviceGetProcessUtilization");
    if (fn == NULL) {
        return CONTRACT_SYMBOL_MISSING;
    }
    return fn(device, utilization, count, lastSeen);
}

static unsigned int contractNearestGpus(contractDevice_t device, unsigned int level,
                                        unsigned int* count, contractDevice_t* deviceArray) {
    unsigned int (*fn)(contractDevice_t, unsigned int, unsigned int*, contractDevice_t*) =
        (unsigned int (*)(contractDevice_t, unsigned int, unsigned int*, contractDevice_t*))
            contractSym("nvmlDeviceGetTopologyNearestGpus");
    if (fn == NULL) {
        return CONTRACT_SYMBOL_MISSING;
    }
    return fn(device, level, count, deviceArray);
}

static unsigned int contractGpuSet(unsigned int cpuNumber, unsigned int* count,
                                   contractDevice_t* deviceArray) {
    unsigned int (*fn)(unsigned int, unsigned int*, contractDevice_t*) =
        (unsigned int (*)(unsigned int, unsigned int*, contractDevice_t*))
            contractSym("nvmlSystemGetTopologyGpuSet");
    if (fn == NULL) {
        return CONTRACT_SYMBOL_MISSING;
    }
    return fn(cpuNumber, count, deviceArray);
}
*/
import "C"

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"
)

// Fixture values from util-test-config.yaml. Devices are picked for what they
// run: 0 has one compute process, 1 has none at all (`processes: []`), and 3
// has the one graphics process in the file.
const (
	contractComputeDevice  = 0
	contractEmptyDevice    = 1
	contractGraphicsDevice = 3

	contractProcessPID = 4242
	contractProcName   = "python"
)

// NVML_TOPOLOGY_SYSTEM. Every peer sits at or below it, so the nearest-GPU
// query has the whole fixture to report and the expected count is just the
// device count less the device itself.
const contractTopoSystem = 50

// contractAffineCPU is on NUMA node 0, which the fixture's pcie_topology gives
// four GPUs.
const (
	contractAffineCPU  = 0
	contractAffineGpus = 4
)

// testArrayContractProbesFromC drives each export's documented probe form from
// C. Everything here needs the fixture's processes and PCIe topology.
func testArrayContractProbesFromC(deviceCount int) []testResult {
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		// Reported rather than dropped: the harness counts the results it is
		// given against no expected total, so a leg that returns nothing is
		// indistinguishable from one that was never wired up.
		return []testResult{skippedResult("contract/abi",
			"needs MOCK_NVML_CONFIG for the fixture's processes and PCIe topology")}
	}

	results := checkRunningProcessProbesFromC()
	results = append(results, checkProcessNameCropFromC()...)
	results = append(results, checkProcessUtilizationProbeFromC()...)
	return append(results, checkTopologyZeroCountFromC(deviceCount)...)
}

// checkRunningProcessProbesFromC covers the probe NVML documents for the two
// running-process queries: `*infoCount = 0` with a NULL `infos`, whose return
// code — not the count — is how the caller learns whether to allocate and call
// again. Answering SUCCESS when processes exist tells a conforming caller there
// are none, and it never makes the filling call. go-nvml cannot see this: it
// always passes a real buffer and grows it on INSUFFICIENT_SIZE.
func checkRunningProcessProbesFromC() []testResult {
	cases := []struct {
		name    string
		symbol  string
		index   int
		wantRet C.uint
		wantCnt C.uint
	}{
		{"contract/abi/compute-processes-present", "nvmlDeviceGetComputeRunningProcesses_v3",
			contractComputeDevice, C.NVML_ERROR_INSUFFICIENT_SIZE, 1},
		{"contract/abi/compute-processes-none", "nvmlDeviceGetComputeRunningProcesses_v3",
			contractEmptyDevice, C.NVML_SUCCESS, 0},
		{"contract/abi/graphics-processes-present", "nvmlDeviceGetGraphicsRunningProcesses_v3",
			contractGraphicsDevice, C.NVML_ERROR_INSUFFICIENT_SIZE, 1},
		{"contract/abi/graphics-processes-none", "nvmlDeviceGetGraphicsRunningProcesses_v3",
			contractEmptyDevice, C.NVML_SUCCESS, 0},
	}

	var results []testResult
	for _, tc := range cases {
		device, err := contractDeviceHandle(tc.index)
		if err != nil {
			results = append(results, testResult{tc.name, false, err.Error()})
			continue
		}

		symbol := C.CString(tc.symbol)
		count := C.uint(0)
		ret := C.contractRunningProcesses(symbol, device, &count, nil)
		C.free(unsafe.Pointer(symbol))

		switch {
		case ret != tc.wantRet:
			results = append(results, testResult{tc.name, false, fmt.Sprintf(
				"%s(*infoCount=0, infos=NULL) on device %d -> %d; want %d: the return code is "+
					"how the header tells a caller whether to allocate and call again",
				tc.symbol, tc.index, ret, tc.wantRet)})
		case count != tc.wantCnt:
			results = append(results, testResult{tc.name, false, fmt.Sprintf(
				"%s probe on device %d reported %d processes; want %d",
				tc.symbol, tc.index, count, tc.wantCnt)})
		default:
			results = append(results, testResult{tc.name, true, ""})
		}
	}
	return results
}

// checkProcessNameCropFromC covers nvmlSystemGetProcessName's short-buffer
// contract: the header says the name is cropped to the length provided and
// does not list INSUFFICIENT_SIZE, so a C caller with a small buffer must get
// a truncated name rather than an error. go-nvml always passes
// SYSTEM_PROCESS_NAME_BUFFER_SIZE, so no Go caller ever hands over a buffer
// this call could overrun.
func checkProcessNameCropFromC() []testResult {
	const bufBytes = 32

	cases := []struct {
		name    string
		length  C.uint
		wantRet C.uint
		wantStr string
	}{
		// One byte of the name plus the terminator have to be dropped, which
		// is the case a caller sizing for a short field name hits.
		{"contract/abi/process-name-cropped", 4, C.NVML_SUCCESS, "pyt"},
		{"contract/abi/process-name-exact", C.uint(len(contractProcName) + 1), C.NVML_SUCCESS, contractProcName},
		// The header names INVALID_ARGUMENT for a zero length specifically,
		// and a caller cannot tell a rejected argument from a buffer it should
		// retry with more space if both come back as INSUFFICIENT_SIZE.
		{"contract/abi/process-name-zero-length", 0, C.NVML_ERROR_INVALID_ARGUMENT, ""},
	}

	var results []testResult
	for _, tc := range cases {
		buf, tailIntact, free := contractBuffer(C.size_t(tc.length), bufBytes-C.size_t(tc.length))
		if buf == nil {
			results = append(results, testResult{tc.name, false, "malloc failed"})
			continue
		}

		ret := C.contractProcessName(contractProcessPID, (*C.char)(buf), tc.length)
		// Read bounded by the length the call was given rather than with
		// GoString: a call that wrote nothing leaves no terminator to stop at,
		// and the guard pattern is not one.
		written := C.GoBytes(buf, C.int(tc.length))
		got := string(written)
		if nul := bytes.IndexByte(written, 0); nul >= 0 {
			got = string(written[:nul])
		}
		tailErr := tailIntact()

		switch {
		case ret != tc.wantRet:
			results = append(results, testResult{tc.name, false, fmt.Sprintf(
				"nvmlSystemGetProcessName(pid=%d, length=%d) -> %d; want %d",
				contractProcessPID, tc.length, ret, tc.wantRet)})
		case tailErr != nil:
			results = append(results, testResult{tc.name, false,
				fmt.Sprintf("nvmlSystemGetProcessName(length=%d): %v", tc.length, tailErr)})
		case tc.wantRet == C.NVML_SUCCESS && got != tc.wantStr:
			results = append(results, testResult{tc.name, false, fmt.Sprintf(
				"nvmlSystemGetProcessName(length=%d) wrote %q; want %q cropped to the buffer",
				tc.length, got, tc.wantStr)})
		default:
			results = append(results, testResult{tc.name, true, ""})
		}
		free()
	}
	return results
}

// checkProcessUtilizationProbeFromC covers both halves of the size probe. The
// header returns NOT_FOUND when no samples exist, which is how a caller
// renders "nothing running" rather than falling through its success path with
// a zero count. INSUFFICIENT_SIZE on the non-empty probe is not in the
// documented return list but is what a real driver answers and what go-nvml
// requires, so it is pinned here too.
func checkProcessUtilizationProbeFromC() []testResult {
	cases := []struct {
		name    string
		index   int
		wantRet C.uint
		wantCnt C.uint
	}{
		{"contract/abi/process-util-no-samples", contractEmptyDevice, C.NVML_ERROR_NOT_FOUND, 0},
		{"contract/abi/process-util-samples", contractComputeDevice, C.NVML_ERROR_INSUFFICIENT_SIZE, 1},
	}

	var results []testResult
	for _, tc := range cases {
		device, err := contractDeviceHandle(tc.index)
		if err != nil {
			results = append(results, testResult{tc.name, false, err.Error()})
			continue
		}

		count := C.uint(0)
		ret := C.contractProcessUtilization(device, nil, &count, 0)
		switch {
		case ret != tc.wantRet:
			results = append(results, testResult{tc.name, false, fmt.Sprintf(
				"nvmlDeviceGetProcessUtilization(utilization=NULL) on device %d -> %d; want %d",
				tc.index, ret, tc.wantRet)})
		case count != tc.wantCnt:
			results = append(results, testResult{tc.name, false, fmt.Sprintf(
				"nvmlDeviceGetProcessUtilization probe on device %d reported %d samples; want %d",
				tc.index, count, tc.wantCnt)})
		default:
			results = append(results, testResult{tc.name, true, ""})
		}
	}
	return append(results, checkProcessUtilizationEmptyFillFromC())
}

// checkProcessUtilizationEmptyFillFromC repeats the empty case with a real
// buffer, which is what a caller reaches after a probe that found samples since
// vanished. The header's NOT_FOUND sentence is unconditional, so both call
// shapes have to agree; go-nvml never gets here, because its wrapper returns
// early on a zero count.
func checkProcessUtilizationEmptyFillFromC() testResult {
	const name = "contract/abi/process-util-empty-fill"

	device, err := contractDeviceHandle(contractEmptyDevice)
	if err != nil {
		return testResult{name, false, err.Error()}
	}

	buf, untouched, free := contractBuffer(0, C.sizeof_nvmlProcessUtilizationSample_t)
	if buf == nil {
		return testResult{name, false, "malloc failed"}
	}
	defer free()

	count := C.uint(1)
	ret := C.contractProcessUtilization(device, (*C.nvmlProcessUtilizationSample_t)(buf), &count, 0)
	switch {
	case ret != C.NVML_ERROR_NOT_FOUND:
		return testResult{name, false, fmt.Sprintf(
			"nvmlDeviceGetProcessUtilization(count=1, buffer supplied) on a device with no "+
				"processes -> %d; want NVML_ERROR_NOT_FOUND", ret)}
	case count != 0:
		return testResult{name, false,
			fmt.Sprintf("reported %d samples alongside NOT_FOUND; want 0", count)}
	}
	if err := untouched(); err != nil {
		return testResult{name, false,
			fmt.Sprintf("nvmlDeviceGetProcessUtilization wrote a sample it has none of: %v", err)}
	}
	return testResult{name, true, ""}
}

// checkTopologyZeroCountFromC covers the size query the two topology array
// queries document — a zero count, whatever the array pointer is — and the
// bounded fill that follows it. go-nvml only ever passes a NULL array for the
// size query, so the form a C caller reaches for first, a stack array with the
// count zeroed, went untested.
func checkTopologyZeroCountFromC(deviceCount int) []testResult {
	device, err := contractDeviceHandle(contractComputeDevice)
	if err != nil {
		return []testResult{{"contract/abi/topology-zero-count", false, err.Error()}}
	}

	wantPeers := C.uint(deviceCount - 1)
	results := []testResult{checkTopologyProbe("contract/abi/nearest-gpus-zero-count",
		"nvmlDeviceGetTopologyNearestGpus", C.size_t(deviceCount), wantPeers,
		func(count *C.uint, array *C.contractDevice_t) C.uint {
			return C.contractNearestGpus(device, contractTopoSystem, count, array)
		})}

	results = append(results, checkTopologyProbe("contract/abi/gpu-set-zero-count",
		"nvmlSystemGetTopologyGpuSet", C.size_t(deviceCount), contractAffineGpus,
		func(count *C.uint, array *C.contractDevice_t) C.uint {
			return C.contractGpuSet(contractAffineCPU, count, array)
		}))

	return append(results, checkNearestGpusBoundedFill(device, wantPeers))
}

// checkTopologyProbe asks one topology query for its count while handing it a
// buffer big enough for the answer. A zero count is the size query however the
// array pointer looks, so the buffer has to come back untouched in full.
func checkTopologyProbe(
	name, symbol string, entries C.size_t, want C.uint,
	call func(*C.uint, *C.contractDevice_t) C.uint,
) testResult {
	arrayBytes := entries * C.sizeof_contractDevice_t
	buf, untouched, free := contractBuffer(0, arrayBytes)
	if buf == nil {
		return testResult{name, false, "malloc failed"}
	}
	defer free()

	count := C.uint(0)
	ret := call(&count, (*C.contractDevice_t)(buf))
	switch {
	case ret != C.NVML_SUCCESS:
		return testResult{name, false, fmt.Sprintf(
			"%s(count=0, deviceArray non-NULL) -> %d; want NVML_SUCCESS: a zero count is the "+
				"size query, whatever the caller's array pointer is", symbol, ret)}
	case count != want:
		return testResult{name, false,
			fmt.Sprintf("%s size query reported %d GPUs; want %d", symbol, count, want)}
	}
	if err := untouched(); err != nil {
		return testResult{name, false, fmt.Sprintf(
			"%s: a size query must not write handles: %v", symbol, err)}
	}
	return testResult{name, true, ""}
}

// checkNearestGpusBoundedFill asks for fewer peers than exist. The header has
// the fill call write count handles, so a caller with room for two must get two
// and nothing beyond them, not an error naming the size it would have needed.
func checkNearestGpusBoundedFill(device C.contractDevice_t, peers C.uint) testResult {
	const name = "contract/abi/nearest-gpus-bounded-fill"

	const room = 2
	if peers <= room {
		return skippedResult(name, fmt.Sprintf(
			"the fixture has %d peers, too few to ask for fewer than all of them", peers))
	}

	buf, tailIntact, free := contractBuffer(room*C.sizeof_contractDevice_t,
		room*C.sizeof_contractDevice_t)
	if buf == nil {
		return testResult{name, false, "malloc failed"}
	}
	defer free()

	count := C.uint(room)
	ret := C.contractNearestGpus(device, contractTopoSystem, &count, (*C.contractDevice_t)(buf))
	switch {
	case ret != C.NVML_SUCCESS:
		return testResult{name, false, fmt.Sprintf(
			"nvmlDeviceGetTopologyNearestGpus(count=%d of %d peers) -> %d; want NVML_SUCCESS: "+
				"INSUFFICIENT_SIZE is not a documented return for this query", room, peers, ret)}
	case count != room:
		return testResult{name, false, fmt.Sprintf(
			"fill call reported %d handles written; want the %d the caller had room for",
			count, room)}
	}
	if err := tailIntact(); err != nil {
		return testResult{name, false,
			fmt.Sprintf("nvmlDeviceGetTopologyNearestGpus: %v", err)}
	}
	for i, handle := range unsafe.Slice((*C.contractDevice_t)(buf), room) {
		if handle == nil {
			return testResult{name, false, fmt.Sprintf("peer %d came back as a NULL handle", i)}
		}
	}
	return testResult{name, true, ""}
}

// contractDeviceHandle fetches a device handle over the same C entry point
// nvidia-smi uses, so a failure here is reported against the leg that needed
// it rather than aborting the sweep.
func contractDeviceHandle(index int) (C.contractDevice_t, error) {
	var device C.contractDevice_t
	if ret := C.contractDeviceHandleByIndex(C.uint(index), &device); ret != C.NVML_SUCCESS {
		return nil, fmt.Errorf("nvmlDeviceGetHandleByIndex_v2(%d) -> %d", index, ret)
	}
	return device, nil
}

// contractBuffer allocates liveBytes the call may write plus guardBytes it may
// not, all filled with a pattern no call may leave behind, and returns the
// buffer, a check on the guard region, and its release. Passing liveBytes zero
// watches the whole allocation, which is what a size query has to leave alone.
func contractBuffer(liveBytes, guardBytes C.size_t) (buf unsafe.Pointer, guard func() error, free func()) {
	total := liveBytes + guardBytes
	mem := C.malloc(total)
	if mem == nil {
		return nil, func() error { return fmt.Errorf("malloc(%d) failed", total) }, func() {}
	}
	C.memset(mem, guardFill, total)

	want := bytes.Repeat([]byte{guardFill}, int(guardBytes))
	return mem, func() error {
			tail := C.GoBytes(unsafe.Add(mem, C.int(liveBytes)), C.int(guardBytes))
			if !bytes.Equal(tail, want) {
				return fmt.Errorf("wrote past the %d bytes it was given", liveBytes)
			}
			return nil
		}, func() {
			C.free(mem)
		}
}
