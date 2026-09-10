// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// MIG instance-list ABI tests. NVML documents the count argument of the four
// MIG array queries as output-only: the caller sizes its buffer from the
// profile and does not report that size back to the library. go-nvml happens to
// preset count to the profile's instance count before calling, so a bridge that
// mistook count for a capacity still satisfied every Go consumer while
// rejecting nvidia-smi, which passes an uninitialized count. Reproducing that
// needs the C entry points called directly, the way nvidia-smi calls them.

package main

/*
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// Layouts pinned against nvml.h. Only the fields these tests read are named
// individually; the rest matter because they set the offsets of those.
typedef struct nvmlDevice_st*          migDevice_t;
typedef struct nvmlGpuInstance_st*     migGpuInstance_t;
typedef struct nvmlComputeInstance_st* migComputeInstance_t;

typedef struct {
    unsigned int start;
    unsigned int size;
} migPlacement_t;

typedef struct {
    unsigned int       id;
    unsigned int       isP2pSupported;
    unsigned int       sliceCount;
    unsigned int       instanceCount;
    unsigned int       multiprocessorCount;
    unsigned int       copyEngineCount;
    unsigned int       decoderCount;
    unsigned int       encoderCount;
    unsigned int       jpegCount;
    unsigned int       ofaCount;
    unsigned long long memorySizeMB;
} migGiProfileInfo_t;

typedef struct {
    unsigned int id;
    unsigned int sliceCount;
    unsigned int instanceCount;
    unsigned int multiprocessorCount;
    unsigned int sharedCopyEngineCount;
    unsigned int sharedDecoderCount;
    unsigned int sharedEncoderCount;
    unsigned int sharedJpegCount;
    unsigned int sharedOfaCount;
} migCiProfileInfo_t;

// Returned instead of an nvmlReturn_t when the symbol itself is missing, so a
// dropped export is distinguishable from a call that failed.
#define MIG_SYMBOL_MISSING 0xFFFFFFFFu

// migSym resolves a public NVML entry point. The library is already loaded
// through go-nvml, so this only fetches a handle to it.
static void* migSym(const char* name) {
    void* lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_GLOBAL);
    if (lib == NULL) {
        return NULL;
    }
    return dlsym(lib, name);
}

static unsigned int migDeviceHandleByIndex(unsigned int index, migDevice_t* device) {
    unsigned int (*fn)(unsigned int, migDevice_t*) =
        (unsigned int (*)(unsigned int, migDevice_t*))migSym("nvmlDeviceGetHandleByIndex_v2");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(index, device);
}

static unsigned int migGiProfileInfo(migDevice_t device, unsigned int profile, migGiProfileInfo_t* info) {
    unsigned int (*fn)(migDevice_t, unsigned int, migGiProfileInfo_t*) =
        (unsigned int (*)(migDevice_t, unsigned int, migGiProfileInfo_t*))
            migSym("nvmlDeviceGetGpuInstanceProfileInfo");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(device, profile, info);
}

static unsigned int migGpuInstances(migDevice_t device, unsigned int profileId,
                                    migGpuInstance_t* instances, unsigned int* count) {
    unsigned int (*fn)(migDevice_t, unsigned int, migGpuInstance_t*, unsigned int*) =
        (unsigned int (*)(migDevice_t, unsigned int, migGpuInstance_t*, unsigned int*))
            migSym("nvmlDeviceGetGpuInstances");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(device, profileId, instances, count);
}

static unsigned int migGiPlacements(migDevice_t device, unsigned int profileId,
                                    migPlacement_t* placements, unsigned int* count) {
    unsigned int (*fn)(migDevice_t, unsigned int, migPlacement_t*, unsigned int*) =
        (unsigned int (*)(migDevice_t, unsigned int, migPlacement_t*, unsigned int*))
            migSym("nvmlDeviceGetGpuInstancePossiblePlacements_v2");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(device, profileId, placements, count);
}

static unsigned int migCiProfileInfo(migGpuInstance_t instance, unsigned int profile,
                                     unsigned int engProfile, migCiProfileInfo_t* info) {
    unsigned int (*fn)(migGpuInstance_t, unsigned int, unsigned int, migCiProfileInfo_t*) =
        (unsigned int (*)(migGpuInstance_t, unsigned int, unsigned int, migCiProfileInfo_t*))
            migSym("nvmlGpuInstanceGetComputeInstanceProfileInfo");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(instance, profile, engProfile, info);
}

static unsigned int migComputeInstances(migGpuInstance_t instance, unsigned int profileId,
                                        migComputeInstance_t* instances, unsigned int* count) {
    unsigned int (*fn)(migGpuInstance_t, unsigned int, migComputeInstance_t*, unsigned int*) =
        (unsigned int (*)(migGpuInstance_t, unsigned int, migComputeInstance_t*, unsigned int*))
            migSym("nvmlGpuInstanceGetComputeInstances");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(instance, profileId, instances, count);
}

static unsigned int migCiPlacements(migGpuInstance_t instance, unsigned int profileId,
                                    migPlacement_t* placements, unsigned int* count) {
    unsigned int (*fn)(migGpuInstance_t, unsigned int, migPlacement_t*, unsigned int*) =
        (unsigned int (*)(migGpuInstance_t, unsigned int, migPlacement_t*, unsigned int*))
            migSym("nvmlGpuInstanceGetComputeInstancePossiblePlacements");
    if (fn == NULL) {
        return MIG_SYMBOL_MISSING;
    }
    return fn(instance, profileId, placements, count);
}
*/
import "C"

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"
)

// Fixture values from util-test-config.yaml: device 2 boots partitioned into
// seven 1g.5gb instances, each holding one spanning compute instance.
const (
	migABIDeviceIndex   = 2
	migABIGpuInstances  = 7
	migABIComputeInsts  = 1
	migABIProfile1Slice = 0 // NVML_GPU_INSTANCE_PROFILE_1_SLICE, also the compute profile
	migABIEngineShared  = 0 // NVML_COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED
)

// migABIGuardEntries is how far past the documented buffer size each list is
// watched for a stray write. The bridge is allowed to write as many entries as
// the profile declares and no more, so the tail must come back as it went in.
const migABIGuardEntries = 4

// testMIGInstanceListsFromC drives the four MIG array queries as a C caller
// does: the buffer is sized from the profile's instance or placement count and
// count is left at zero, because NVML never reads it as an input. A bridge that
// checks it as a capacity answers ERROR_INSUFFICIENT_SIZE here, which is what
// `nvidia-smi mig -lgi/-lci/-lcip` reports as "Insufficient Size".
func testMIGInstanceListsFromC() []testResult {
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		return nil // fixture-driven: the env config leaves every device unpartitioned
	}

	var device C.migDevice_t
	if ret := C.migDeviceHandleByIndex(migABIDeviceIndex, &device); ret != 0 {
		return []testResult{{"mig/abi/device", false,
			fmt.Sprintf("nvmlDeviceGetHandleByIndex_v2(%d) -> %d", migABIDeviceIndex, ret)}}
	}

	var giInfo C.migGiProfileInfo_t
	if ret := C.migGiProfileInfo(device, migABIProfile1Slice, &giInfo); ret != 0 {
		return []testResult{{"mig/abi/gi-profile", false,
			fmt.Sprintf("nvmlDeviceGetGpuInstanceProfileInfo -> %d", ret)}}
	}
	if giInfo.instanceCount != migABIGpuInstances {
		return []testResult{{"mig/abi/gi-profile", false, fmt.Sprintf(
			"profile declares %d instances; want %d (the buffer size every C caller derives)",
			giInfo.instanceCount, migABIGpuInstances)}}
	}

	results, instances := checkGpuInstancesFromC(device, &giInfo)
	results = append(results, checkGiPlacementsFromC(device, &giInfo)...)
	if len(instances) == 0 {
		return results
	}
	return append(results, testComputeInstanceListsFromC(instances[0])...)
}

// checkGpuInstancesFromC lists a device's GPU instances into a
// profile-sized buffer and returns the handles for the compute-instance leg.
func checkGpuInstancesFromC(
	device C.migDevice_t, giInfo *C.migGiProfileInfo_t,
) ([]testResult, []C.migGpuInstance_t) {
	const name = "mig/abi/gpu-instances"

	buf, guard, free := migABIBuffer(C.size_t(giInfo.instanceCount), C.sizeof_migGpuInstance_t)
	defer free()

	count := C.uint(0)
	ret := C.migGpuInstances(device, giInfo.id, (*C.migGpuInstance_t)(buf), &count)
	if ret != 0 {
		return []testResult{{name, false, fmt.Sprintf(
			"nvmlDeviceGetGpuInstances with count=0 on input -> %d; want NVML_SUCCESS: NVML "+
				"documents count as output-only, so nvidia-smi never sets it", ret)}}, nil
	}
	if count != migABIGpuInstances {
		return []testResult{{name, false,
			fmt.Sprintf("nvmlDeviceGetGpuInstances -> %d instances; want %d", count, migABIGpuInstances)}}, nil
	}
	if err := guard(); err != nil {
		return []testResult{{name, false, fmt.Sprintf("nvmlDeviceGetGpuInstances: %v", err)}}, nil
	}

	instances := make([]C.migGpuInstance_t, 0, count)
	for i, handle := range unsafe.Slice((*C.migGpuInstance_t)(buf), count) {
		if handle == nil {
			return []testResult{{name, false, fmt.Sprintf("instance %d came back as a NULL handle", i)}}, nil
		}
		instances = append(instances, handle)
	}
	return []testResult{{name, true, ""}}, instances
}

// checkGiPlacementsFromC covers both forms NVML documents for the GPU instance
// placement query: a NULL array to discover the count, and a profile-sized
// array with count zero on input.
func checkGiPlacementsFromC(device C.migDevice_t, giInfo *C.migGiProfileInfo_t) []testResult {
	const name = "mig/abi/gi-placements"

	probe := C.uint(0)
	if ret := C.migGiPlacements(device, giInfo.id, nil, &probe); ret != 0 || probe == 0 {
		return []testResult{{name, false, fmt.Sprintf(
			"NULL-array probe -> %d placements, ret=%d; want a count and NVML_SUCCESS", probe, ret)}}
	}

	buf, guard, free := migABIBuffer(C.size_t(probe), C.sizeof_migPlacement_t)
	defer free()

	count := C.uint(0)
	ret := C.migGiPlacements(device, giInfo.id, (*C.migPlacement_t)(buf), &count)
	switch {
	case ret != 0:
		return []testResult{{name, false, fmt.Sprintf(
			"nvmlDeviceGetGpuInstancePossiblePlacements_v2 with count=0 on input -> %d; want NVML_SUCCESS", ret)}}
	case count != probe:
		return []testResult{{name, false, fmt.Sprintf(
			"fill call reported %d placements; the probe reported %d", count, probe)}}
	}
	if err := guard(); err != nil {
		return []testResult{{name, false, fmt.Sprintf("nvmlDeviceGetGpuInstancePossiblePlacements_v2: %v", err)}}
	}
	return []testResult{{name, true, ""}}
}

// testComputeInstanceListsFromC repeats the two checks one level down, inside a
// GPU instance, where nvidia-smi's -lci and -lcip land.
func testComputeInstanceListsFromC(instance C.migGpuInstance_t) []testResult {
	var ciInfo C.migCiProfileInfo_t
	if ret := C.migCiProfileInfo(instance, migABIProfile1Slice, migABIEngineShared, &ciInfo); ret != 0 {
		return []testResult{{"mig/abi/ci-profile", false,
			fmt.Sprintf("nvmlGpuInstanceGetComputeInstanceProfileInfo -> %d", ret)}}
	}

	results := checkComputeInstancesFromC(instance, &ciInfo)
	return append(results, checkCiPlacementsFromC(instance, &ciInfo)...)
}

// checkComputeInstancesFromC lists a GPU instance's compute instances into a
// profile-sized buffer with count zero on input.
func checkComputeInstancesFromC(instance C.migGpuInstance_t, ciInfo *C.migCiProfileInfo_t) []testResult {
	const name = "mig/abi/compute-instances"

	buf, guard, free := migABIBuffer(C.size_t(ciInfo.instanceCount), C.sizeof_migComputeInstance_t)
	defer free()

	count := C.uint(0)
	ret := C.migComputeInstances(instance, ciInfo.id, (*C.migComputeInstance_t)(buf), &count)
	switch {
	case ret != 0:
		return []testResult{{name, false, fmt.Sprintf(
			"nvmlGpuInstanceGetComputeInstances with count=0 on input -> %d; want NVML_SUCCESS", ret)}}
	case count != migABIComputeInsts:
		return []testResult{{name, false, fmt.Sprintf(
			"nvmlGpuInstanceGetComputeInstances -> %d instances; want %d", count, migABIComputeInsts)}}
	}
	if err := guard(); err != nil {
		return []testResult{{name, false, fmt.Sprintf("nvmlGpuInstanceGetComputeInstances: %v", err)}}
	}
	for i, handle := range unsafe.Slice((*C.migComputeInstance_t)(buf), count) {
		if handle == nil {
			return []testResult{{name, false, fmt.Sprintf("compute instance %d came back as a NULL handle", i)}}
		}
	}
	return []testResult{{name, true, ""}}
}

// checkCiPlacementsFromC covers the NULL probe and the count-zero fill call for
// the compute instance placement query.
func checkCiPlacementsFromC(instance C.migGpuInstance_t, ciInfo *C.migCiProfileInfo_t) []testResult {
	const name = "mig/abi/ci-placements"

	probe := C.uint(0)
	if ret := C.migCiPlacements(instance, ciInfo.id, nil, &probe); ret != 0 || probe == 0 {
		return []testResult{{name, false, fmt.Sprintf(
			"NULL-array probe -> %d placements, ret=%d; want a count and NVML_SUCCESS", probe, ret)}}
	}

	buf, guard, free := migABIBuffer(C.size_t(probe), C.sizeof_migPlacement_t)
	defer free()

	count := C.uint(0)
	ret := C.migCiPlacements(instance, ciInfo.id, (*C.migPlacement_t)(buf), &count)
	switch {
	case ret != 0:
		return []testResult{{name, false, fmt.Sprintf(
			"nvmlGpuInstanceGetComputeInstancePossiblePlacements with count=0 on input -> %d; want NVML_SUCCESS", ret)}}
	case count != probe:
		return []testResult{{name, false, fmt.Sprintf(
			"fill call reported %d placements; the probe reported %d", count, probe)}}
	}
	if err := guard(); err != nil {
		return []testResult{{name, false,
			fmt.Sprintf("nvmlGpuInstanceGetComputeInstancePossiblePlacements: %v", err)}}
	}
	return []testResult{{name, true, ""}}
}

// migABIBuffer allocates entries elements plus a guard tail filled with a
// pattern no call may leave behind, and returns the buffer, a check on the
// tail, and its release. The tail is what proves the bridge stops at the
// documented buffer size instead of trusting its own idea of how many entries
// there are.
func migABIBuffer(entries, entrySize C.size_t) (buf unsafe.Pointer, guard func() error, free func()) {
	total := (entries + migABIGuardEntries) * entrySize
	mem := C.malloc(total)
	if mem == nil {
		return nil, func() error { return fmt.Errorf("malloc(%d) failed", total) }, func() {}
	}
	C.memset(mem, guardFill, total)

	tailOffset := C.int(entries * entrySize)
	tailSize := C.int(migABIGuardEntries * entrySize)
	want := bytes.Repeat([]byte{guardFill}, int(tailSize))
	return mem, func() error {
			tail := C.GoBytes(unsafe.Add(mem, tailOffset), tailSize)
			if !bytes.Equal(tail, want) {
				return fmt.Errorf("wrote past the %d entries the profile declares", entries)
			}
			return nil
		}, func() {
			C.free(mem)
		}
}
