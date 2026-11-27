// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#include "nvml_types.h"
*/
import "C"
import "unsafe"

// platformIbGUIDForTest calls nvmlDeviceGetPlatformInfo with the caller's
// version stamped in, as nvidia-smi does, and returns the ibGuid bytes the
// bridge wrote. Both struct versions keep ibGuid at the same offset, so one
// buffer serves either.
func platformIbGUIDForTest(handle unsafe.Pointer, version uint32) ([]byte, uint32) {
	info := C.nvmlPlatformInfo_t{version: C.uint(version)}
	ret := nvmlDeviceGetPlatformInfo(deviceForTest(handle), &info)
	out := make([]byte, len(info.ibGuid))
	for i, b := range info.ibGuid {
		out[i] = byte(b)
	}
	return out, uint32(ret)
}
