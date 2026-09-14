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

// Package main provides the NVML power limit setters. The four power getters
// have always been implemented, so the mock would report a cap and then decline
// to change it: `nvidia-smi -pl` failed and a capping controller's
// read-after-write saw its request silently ignored.
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

// powerValueV2Version is the tag a caller stamps into
// nvmlPowerValue_v2_t.version, encoding the struct version and its size the way
// the upstream NVML_STRUCT_VERSION macro does.
func powerValueV2Version() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlPowerValue_v2_t{}), 2)
}

//export nvmlDeviceSetPowerManagementLimit
func nvmlDeviceSetPowerManagementLimit(device C.nvmlDevice_t, limit C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetPowerManagementLimit"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetPowerManagementLimit(uint32(limit)))
}

//export nvmlDeviceSetPowerManagementLimit_v2
func nvmlDeviceSetPowerManagementLimit_v2(device C.nvmlDevice_t, powerValue *C.nvmlPowerValue_v2_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetPowerManagementLimit_v2"); !ok {
		return ret
	}
	if powerValue == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// Unlike the versioned getters, this struct is read-only here, so a wrong
	// tag cannot overrun the caller's buffer and upstream does not document
	// ARGUMENT_VERSION_MISMATCH for this entry point. An unstamped zero is
	// therefore accepted — go-nvml callers pass the struct through without
	// filling it in — while a tag that is present but wrong is a caller built
	// against a layout we cannot read, which upstream folds into
	// "contains invalid values".
	if v := uint32(powerValue.version); v != 0 && v != powerValueV2Version() {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetPowerManagementLimit_v2(&nvml.PowerValue_v2{
		Version:      uint32(powerValue.version),
		PowerScope:   uint8(powerValue.powerScope),
		PowerValueMw: uint32(powerValue.powerValueMw),
	}))
}
