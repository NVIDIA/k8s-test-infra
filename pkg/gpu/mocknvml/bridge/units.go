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

// Package main provides NVML's unit enumeration.
//
// Units are the S-class rack enclosures NVML was originally written for. No
// profile the mock ships models one, so the only unit call it answers is the
// count, and the honest answer is zero — the same answer a real driver gives
// on every modern DGX/HGX/NVL node.
//
// Reporting it matters beyond completeness: `nvidia-smi -q -u` reads the unit
// count first and aborts with "Unable to determine number of available units"
// if the call fails, which takes the HIC block (nvmlSystemGetHicVersion) down
// with it. The remaining nvmlUnit* calls stay stubbed: with no units to name,
// no caller can obtain a unit handle to pass them.
package main

/*
#include "nvml_types.h"
*/
import "C"

//export nvmlUnitGetCount
func nvmlUnitGetCount(unitCount *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlUnitGetCount"); !ok {
		return ret
	}
	if unitCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	*unitCount = 0
	debugLog("[NVML] nvmlUnitGetCount -> 0\n")
	return C.NVML_SUCCESS
}

// unitCountForTest drives the export the way a caller does, since a _test.go
// file in this package cannot name C.uint.
func unitCountForTest(nilOut bool) (uint32, uint32) {
	if nilOut {
		return 0, uint32(nvmlUnitGetCount(nil))
	}
	var count C.uint
	status := uint32(nvmlUnitGetCount(&count))
	return uint32(count), status
}
