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

// Package main provides the NVML NVLink Reduced Bandwidth Mode (RBM) exports:
// the per-device supported-list / get / set trio and the node-wide get / set
// pair.
//
// These back `nvidia-smi nvlink -gBwMode`, `-sBwMode`, and `-sBwMode values`.
// The mode values are opaque driver indices; the nvidia-smi the mock image
// bundles names them 0=FULL, 1=OFF, 2=MIN, 3=HALF, 4=3QUARTER, which is why
// the engine never reports a value above 4 — a higher one would index past
// that binary's name table.
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

// nvlinkStructVersionOK reports whether the caller's version field matches the
// one layout that exists for these structs, logging the rejection.
//
// The v1 size is a parameter rather than derived because every struct in this
// family is a distinct C type; keeping the rule in one pure-Go function makes
// it testable without cgo, which test files in this package cannot use.
func nvlinkStructVersionOK(funcName string, requested uint32, v1Size uintptr) bool {
	v1Tag := FabricStructVersion(v1Size, 1)
	if requested == v1Tag {
		return true
	}
	debugLog("[NVML] %s rejected struct version 0x%x (v1=0x%x)\n", funcName, requested, v1Tag)
	return false
}

// nvlinkInfoStructVersionOK is the same rule for nvmlNvLinkInfo_t, the one
// struct in this family with two upstream layouts. Upstream aliases
// nvmlNvLinkInfo_t to v2, so a caller built against NVML 13 — including the
// nvidia-smi the mock image bundles — sends the v2 tag; rejecting it would
// fail `nvidia-smi nvlink --info` outright. Accepting both is safe because v2
// only appends firmwareInfo, leaving the two fields the mock writes at the
// offsets v1 already has.
func nvlinkInfoStructVersionOK(funcName string, requested uint32, v1Size, v2Size uintptr) bool {
	v1Tag := FabricStructVersion(v1Size, 1)
	v2Tag := FabricStructVersion(v2Size, 2)
	if requested == v1Tag || requested == v2Tag {
		return true
	}
	debugLog("[NVML] %s rejected struct version 0x%x (v1=0x%x, v2=0x%x)\n",
		funcName, requested, v1Tag, v2Tag)
	return false
}

//export nvmlDeviceGetNvlinkSupportedBwModes
func nvmlDeviceGetNvlinkSupportedBwModes(device C.nvmlDevice_t, supportedBwMode *C.nvmlNvlinkSupportedBwModes_t) C.nvmlReturn_t {
	if supportedBwMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvlinkSupportedBwModes"); !ok {
		return ret
	}
	if !nvlinkStructVersionOK("nvmlDeviceGetNvlinkSupportedBwModes",
		uint32(supportedBwMode.version), unsafe.Sizeof(C.nvmlNvlinkSupportedBwModes_v1_t{})) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	modes, ret := dev.GetMockNvlinkSupportedBwModes()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	// Write every byte on purpose: the struct arrives uninitialised, so the
	// tail past totalBwModes would otherwise read as caller garbage.
	for i := range supportedBwMode.bwModes {
		supportedBwMode.bwModes[i] = 0
	}
	n := len(modes)
	if n > len(supportedBwMode.bwModes) {
		n = len(supportedBwMode.bwModes)
	}
	for i := 0; i < n; i++ {
		supportedBwMode.bwModes[i] = C.uchar(modes[i])
	}
	supportedBwMode.totalBwModes = C.uchar(n)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvlinkBwMode
func nvmlDeviceGetNvlinkBwMode(device C.nvmlDevice_t, getBwMode *C.nvmlNvlinkGetBwMode_t) C.nvmlReturn_t {
	if getBwMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvlinkBwMode"); !ok {
		return ret
	}
	if !nvlinkStructVersionOK("nvmlDeviceGetNvlinkBwMode",
		uint32(getBwMode.version), unsafe.Sizeof(C.nvmlNvlinkGetBwMode_v1_t{})) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mode, isBest, ret := dev.GetMockNvlinkBwMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	getBwMode.bwMode = C.uchar(mode)
	getBwMode.bIsBest = 0
	if isBest {
		getBwMode.bIsBest = 1
	}
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetNvlinkBwMode
func nvmlDeviceSetNvlinkBwMode(device C.nvmlDevice_t, setBwMode *C.nvmlNvlinkSetBwMode_t) C.nvmlReturn_t {
	if setBwMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetNvlinkBwMode"); !ok {
		return ret
	}
	if !nvlinkStructVersionOK("nvmlDeviceSetNvlinkBwMode",
		uint32(setBwMode.version), unsafe.Sizeof(C.nvmlNvlinkSetBwMode_v1_t{})) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetMockNvlinkBwMode(uint8(setBwMode.bwMode), setBwMode.bSetBest != 0))
}

//export nvmlSystemGetNvlinkBwMode
func nvmlSystemGetNvlinkBwMode(nvlinkBwMode *C.uint) C.nvmlReturn_t {
	if nvlinkBwMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemGetNvlinkBwMode"); !ok {
		return ret
	}
	mode, ret := engine.GetEngine().SystemGetNvlinkBwMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*nvlinkBwMode = C.uint(mode)
	return C.NVML_SUCCESS
}

//export nvmlSystemSetNvlinkBwMode
func nvmlSystemSetNvlinkBwMode(nvlinkBwMode C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemSetNvlinkBwMode"); !ok {
		return ret
	}
	return toReturn(engine.GetEngine().SystemSetNvlinkBwMode(uint32(nvlinkBwMode)))
}
