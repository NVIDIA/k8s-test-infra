// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

/*
#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Tests cannot import C. These wrappers exercise the exported functions with
// their C output types, including the retired-page count-only probe.
func featureReadForTest(name string, handle unsafe.Pointer, nullOutput bool) (uint32, nvml.Return) {
	device := C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
	out := C.uint(0xdeadbeef)
	ptr := &out
	if nullOutput {
		ptr = nil
	}
	var ret C.nvmlReturn_t
	switch name {
	case "gom":
		ret = nvmlDeviceGetGpuOperationMode(device, (*C.nvmlGpuOperationMode_t)(unsafe.Pointer(ptr)), (*C.nvmlGpuOperationMode_t)(unsafe.Pointer(ptr)))
	case "retired_v1":
		ret = nvmlDeviceGetRetiredPages(device, 0, ptr, nil)
	case "retired_v2":
		ret = nvmlDeviceGetRetiredPages_v2(device, 1, ptr, nil, nil)
	case "retired_pending":
		ret = nvmlDeviceGetRetiredPagesPendingStatus(device, (*C.nvmlEnableState_t)(unsafe.Pointer(ptr)))
	case "target":
		ret = nvmlDeviceGetTemperatureThreshold(device, C.nvmlTemperatureThresholds_t(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR), ptr)
	case "dram":
		info := C.nvmlDramEncryptionInfo_t{version: C.uint(dramEncryptionVersion()), encryptionState: out}
		p := &info
		if nullOutput {
			p = nil
		}
		ret = nvmlDeviceGetDramEncryptionMode(device, p, p)
		out = info.encryptionState
	default:
		panic("unknown feature test query: " + name)
	}
	return uint32(out), nvml.Return(ret)
}

func dramVersionForTest(handle unsafe.Pointer) nvml.Return {
	device := C.nvmlDevice_t{handle: (*C.struct_nvmlDevice_st)(handle)}
	info := C.nvmlDramEncryptionInfo_t{}
	return nvml.Return(nvmlDeviceGetDramEncryptionMode(device, &info, &info))
}
