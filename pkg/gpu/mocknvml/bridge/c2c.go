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

// Package main provides the NVML C2C (chip-to-chip) mode function.
// nvmlDeviceGetC2cModeInfoV answers whether the GPU has an NVLink-C2C link to
// the host CPU — the defining feature of Grace-Blackwell boards, which
// `nvidia-smi -q` renders as "GPU C2C Mode".
//
// Despite the V suffix, nvmlC2cModeInfo_v1_t carries no version field, so
// there is deliberately no version dispatch here of the kind fabric.go needs.
// See issue NVIDIA/k8s-test-infra#639.
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

//export nvmlDeviceGetC2cModeInfoV
func nvmlDeviceGetC2cModeInfoV(device C.nvmlDevice_t, c2cModeInfo *C.nvmlC2cModeInfo_v1_t) C.nvmlReturn_t {
	if c2cModeInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	handle := unsafe.Pointer(device.handle)
	dev, ok := engine.GetEngine().LookupDevice(handle).(*engine.ConfigurableDevice)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	enabled, ret := dev.GetMockC2cMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	c2cModeInfo.isC2cEnabled = 0
	if enabled {
		c2cModeInfo.isC2cEnabled = 1
	}
	return C.NVML_SUCCESS
}
