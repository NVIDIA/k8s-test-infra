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

// Package main provides NVML NVLink bridge functions not already carried by
// device.go (state / version / capability / error / remote-PCI live there).
// This file adds the remote-device-type, utilization-counter, and
// freeze/reset exports. All are thin marshalling over the pure-Go engine,
// whose values derive from the immutable NodeFabric.

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

//export nvmlDeviceGetNvLinkRemoteDeviceType
func nvmlDeviceGetNvLinkRemoteDeviceType(device C.nvmlDevice_t, link C.uint, pNvLinkDeviceType *C.nvmlIntNvLinkDeviceType_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkRemoteDeviceType"); !ok {
		return ret
	}
	if pNvLinkDeviceType == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	t, ret := dev.GetNvLinkRemoteDeviceType(int(link))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*pNvLinkDeviceType = C.nvmlIntNvLinkDeviceType_t(t)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetNvLinkUtilizationCounter
func nvmlDeviceGetNvLinkUtilizationCounter(device C.nvmlDevice_t, link C.uint, counter C.uint, rxcounter *C.ulonglong, txcounter *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetNvLinkUtilizationCounter"); !ok {
		return ret
	}
	if rxcounter == nil || txcounter == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	rx, tx, ret := dev.GetNvLinkUtilizationCounter(int(link), int(counter))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*rxcounter = C.ulonglong(rx)
	*txcounter = C.ulonglong(tx)
	return C.NVML_SUCCESS
}

//export nvmlDeviceFreezeNvLinkUtilizationCounter
func nvmlDeviceFreezeNvLinkUtilizationCounter(device C.nvmlDevice_t, link C.uint, counter C.uint, freeze C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceFreezeNvLinkUtilizationCounter"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.FreezeNvLinkUtilizationCounter(int(link), int(counter), nvml.EnableState(freeze)))
}

//export nvmlDeviceResetNvLinkUtilizationCounter
func nvmlDeviceResetNvLinkUtilizationCounter(device C.nvmlDevice_t, link C.uint, counter C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceResetNvLinkUtilizationCounter"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.ResetNvLinkUtilizationCounter(int(link), int(counter)))
}

//export nvmlDeviceResetNvLinkErrorCounters
func nvmlDeviceResetNvLinkErrorCounters(device C.nvmlDevice_t, link C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceResetNvLinkErrorCounters"); !ok {
		return ret
	}
	dev := engine.GetEngine().LookupConfigurableDevice(uintptr(unsafe.Pointer(device.handle)))
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.ResetNvLinkErrorCounters(int(link)))
}
