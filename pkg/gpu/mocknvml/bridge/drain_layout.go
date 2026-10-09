// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#include "nvml_types.h"
*/
import "C"
import "unsafe"

func pciInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlPciInfo_t{})
}

func excludedDeviceInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlExcludedDeviceInfo_t{})
}

func excludedDeviceInfoUUIDOffsetForTest() uintptr {
	var info C.nvmlExcludedDeviceInfo_t
	return unsafe.Offsetof(info.uuid)
}
