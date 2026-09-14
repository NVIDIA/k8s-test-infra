// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1 driven through the raw C ABI.
//
// This module's go-nvml is pinned at v0.13.0-1, which predates the binding for
// this entry point — and the pin is deliberate, because the throttle field ids
// move in later versions (see checkThrottleFieldIDs). So the call is made by
// symbol, with the struct laid out here from nvml.h rather than borrowed from
// go-nvml.
//
// That independence is the point. Of the three requested-profile setters this
// is the only one whose struct carries no version member, so nothing but the
// layout itself catches the mask being read at the wrong offset: the operation
// is a 4-byte enum and the mask follows immediately after it.

package main

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <string.h>

typedef struct {
    unsigned int mask[8];
} mockMask255;

// Layout of nvmlWorkloadPowerProfileUpdateProfiles_v1_t: a 4-byte operation
// enum followed by the 32-byte mask, with no version field.
typedef struct {
    unsigned int operation;
    mockMask255  updateProfilesMask;
} mockUpdateProfiles_v1;

#define MOCK_UPDATE_NO_LIB    0xFFFFFFFFu
#define MOCK_UPDATE_NO_SYMBOL 0xFFFFFFFEu

// mockWorkloadUpdateProfiles resolves its own device handle rather than
// borrowing go-nvml's, so nothing about this call path depends on the pinned
// bindings. The library is already loaded and initialised by the time the
// tests run, so this dlopen only fetches a handle to it.
//
// profileId >= 255 names no profile, which is how an empty mask is requested.
static unsigned int mockWorkloadUpdateProfiles(unsigned int index, unsigned int operation, unsigned int profileId) {
    void* lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_GLOBAL);
    if (lib == NULL) {
        return MOCK_UPDATE_NO_LIB;
    }
    unsigned int (*getHandle)(unsigned int, void**) =
        (unsigned int (*)(unsigned int, void**))dlsym(lib, "nvmlDeviceGetHandleByIndex_v2");
    unsigned int (*update)(void*, mockUpdateProfiles_v1*) =
        (unsigned int (*)(void*, mockUpdateProfiles_v1*))dlsym(lib, "nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1");
    if (getHandle == NULL || update == NULL) {
        return MOCK_UPDATE_NO_SYMBOL;
    }

    void* device = NULL;
    unsigned int ret = getHandle(index, &device);
    if (ret != 0) {
        return ret;
    }

    mockUpdateProfiles_v1 req;
    memset(&req, 0, sizeof(req));
    req.operation = operation;
    if (profileId < 255) {
        req.updateProfilesMask.mask[profileId / 32] |= 1u << (profileId % 32);
    }
    return update(device, &req);
}
*/
import "C"

import (
	"errors"
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// The operation values and the "no profile" sentinel, spelled out from nvml.h
// because the pinned go-nvml has neither the enum nor a binding for this entry
// point. Defining them here is what keeps this check independent of the
// bindings it is meant to cross-check.
type profileOperation uint32

const (
	operationClear           profileOperation = 0
	operationSet             profileOperation = 1
	operationSetAndOverwrite profileOperation = 2

	// noProfile names no profile, producing an empty mask.
	noProfile uint32 = 255
)

// updateProfilesRaw calls the non-deprecated setter by symbol and maps the two
// resolution failures onto errors that name themselves, so a missing export is
// not reported as an NVML return code it never produced.
func updateProfilesRaw(index int, op profileOperation, profileID uint32) (nvml.Return, error) {
	ret := C.mockWorkloadUpdateProfiles(C.uint(index), C.uint(op), C.uint(profileID))
	switch uint32(ret) {
	case 0xFFFFFFFF:
		return nvml.ERROR_UNKNOWN, errors.New("dlopen libnvidia-ml.so.1 failed")
	case 0xFFFFFFFE:
		return nvml.ERROR_UNKNOWN, errors.New(
			"nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1 is not exported by the built library")
	}
	return nvml.Return(ret), nil
}

// testUpdateProfilesV1ABI drives the non-deprecated setter through the raw C
// ABI and reads each write back through go-nvml, which is what proves the
// struct was understood: a mask read at the wrong offset still returns SUCCESS,
// and only the read-back shows that nothing (or the wrong profile) landed.
func testUpdateProfilesV1ABI(index int, device nvml.Device, supported []uint32) []testResult {
	name := fmt.Sprintf("workloadprofiles/gpu%d/update_v1_abi", index)
	if len(supported) == 0 {
		return []testResult{{name, true, ""}}
	}
	target := supported[0]

	steps := []struct {
		label string
		op    profileOperation
		id    uint32
		want  []uint32
	}{
		{"overwrite", operationSetAndOverwrite, target, []uint32{target}},
		{"clear", operationClear, target, nil},
		{"set", operationSet, target, []uint32{target}},
		{"overwrite_none", operationSetAndOverwrite, noProfile, nil},
	}

	var results []testResult
	for _, step := range steps {
		ret, err := updateProfilesRaw(index, step.op, step.id)
		if err != nil {
			results = append(results, testResult{name + "/" + step.label, false, err.Error()})
			continue
		}
		results = append(results, checkRequestedProfiles(name+"/"+step.label, device, ret, step.want))
	}

	// An unadvertised profile must be refused here too: the deprecated
	// setters and this one share the engine's validation, and a mask decoded
	// at the wrong offset would most likely name nothing and be accepted.
	ret, err := updateProfilesRaw(index, operationSet, 254)
	switch {
	case err != nil:
		results = append(results, testResult{name + "/reject_unsupported", false, err.Error()})
	case ret != nvml.ERROR_INVALID_ARGUMENT:
		results = append(results, testResult{name + "/reject_unsupported", false,
			fmt.Sprintf("requesting an unadvertised profile returned %v, want INVALID_ARGUMENT",
				nvml.ErrorString(ret))})
	default:
		results = append(results, testResult{name + "/reject_unsupported", true, ""})
	}
	return results
}
