// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

// Package main provides NVML initialization and shutdown functions.
// This file contains the hand-written implementations for:
// - nvmlInit, nvmlInit_v1, nvmlInit_v2
// - nvmlInitWithFlags
// - nvmlShutdown

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"
*/
import "C"
import (
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

//export nvmlInit_v2
func nvmlInit_v2() C.nvmlReturn_t {
	ret := engine.GetEngine().Init()
	return toReturn(ret)
}

//export nvmlInitWithFlags
func nvmlInitWithFlags(flags C.uint) C.nvmlReturn_t {
	// NVML_INIT_FLAG_NO_GPUS (1) - Allow init when no GPUs
	// NVML_INIT_FLAG_NO_ATTACH (2) - Don't enumerate GPUs on init
	// Mock ignores these since we always have simulated GPUs
	if flags != 0 {
		debugLog("[NVML] nvmlInitWithFlags: flags=0x%x ignored (mock always has GPUs)\n", flags)
	}
	ret := engine.GetEngine().Init()
	return toReturn(ret)
}

//export nvmlShutdown
func nvmlShutdown() C.nvmlReturn_t {
	ret := engine.GetEngine().Shutdown()
	// Clear cached error strings to prevent memory leaks
	ClearErrorStringCache()
	return toReturn(ret)
}

// Non-versioned symbol aliases for nvidia-smi compatibility
// These symbols are required because nvidia-smi looks for unversioned symbols

//export nvmlInit
func nvmlInit() C.nvmlReturn_t {
	return nvmlInit_v2()
}

//export nvmlInit_v1
func nvmlInit_v1() C.nvmlReturn_t {
	// v1 calls v2 - same behavior in mock
	return nvmlInit_v2()
}
