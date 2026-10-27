// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#include "nvml_types.h"
*/
import "C"
import "unsafe"

func encoderSessionInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlEncoderSessionInfo_t{})
}

func fbcSessionInfoSizeForTest() uintptr {
	return unsafe.Sizeof(C.nvmlFBCSessionInfo_t{})
}
