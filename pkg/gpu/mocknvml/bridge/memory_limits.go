// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#include "nvml_types.h"
*/
import "C"

// nvmlDeviceSetMemoryLimits_v1 acknowledges the container toolkit's GPU-memory
// limit hook so fractional containers can start against simulated GPUs. Limits
// are neither stored nor enforced; this is not evidence of memory isolation.
//
//export nvmlDeviceSetMemoryLimits_v1
func nvmlDeviceSetMemoryLimits_v1(device C.nvmlDevice_t, limits *C.nvmlSetMemoryLimits_v1_t) C.nvmlReturn_t {
	_, _ = device, limits
	return C.NVML_SUCCESS
}
