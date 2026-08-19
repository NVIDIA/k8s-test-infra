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

// Package main provides the NVML platform-identity functions:
//   - nvmlDeviceGetPlatformInfo (versioned, dispatches on the version field)
//   - nvmlDeviceGetModuleId
//
// These answer where a board physically sits in a rack, which `nvidia-smi -q`
// renders as its "Platform Info" block. On NVL72 that is what turns a GPU
// fault into an actionable location — module 2 of the tray in slot 9 of a given
// chassis — so rack-scale fault correlation cannot be tested against the mock
// without them. See issue NVIDIA/k8s-test-infra#642.
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

//export nvmlDeviceGetPlatformInfo
func nvmlDeviceGetPlatformInfo(device C.nvmlDevice_t, platformInfo *C.nvmlPlatformInfo_t) C.nvmlReturn_t {
	if platformInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetPlatformInfo"); !ok {
		return ret
	}
	dev, ok := lookupPlatformDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetMockPlatformInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}

	// The caller selects the struct version by writing into the `version`
	// field before calling. v1 and v2 cover the same 44 bytes under different
	// names, so both are served from one payload; anything else — including a
	// zero/unset version — gets ARGUMENT_VERSION_MISMATCH, as the upstream
	// header documents.
	requested := uint32(platformInfo.version)
	v1Tag := FabricStructVersion(unsafe.Sizeof(C.nvmlPlatformInfo_v1_t{}), 1)
	v2Tag := FabricStructVersion(unsafe.Sizeof(C.nvmlPlatformInfo_v2_t{}), 2)

	switch ClassifyPlatformVersion(requested, v1Tag, v2Tag) {
	case PlatformVersionV1:
		// Reinterpret as v1, whose rackGuid occupies the bytes v2 calls
		// chassisSerialNumber: on Blackwell the rack is identified by that
		// same serial, which is why the rename was ABI-compatible.
		v1info := (*C.nvmlPlatformInfo_v1_t)(unsafe.Pointer(platformInfo))
		v1info.version = C.uint(v1Tag)
		fillUchars(v1info.rackGuid[:], info.ChassisSerialNumber[:])
		fillUchars(v1info.ibGuid[:], nil)
		v1info.chassisPhysicalSlotNumber = C.uchar(info.SlotNumber)
		v1info.computeSlotIndex = C.uchar(info.TrayIndex)
		v1info.nodeIndex = C.uchar(info.HostID)
		v1info.peerType = C.uchar(info.PeerType)
		v1info.moduleId = C.uchar(info.ModuleID)
		return C.NVML_SUCCESS
	case PlatformVersionV2:
		platformInfo.version = C.uint(v2Tag)
		fillUchars(platformInfo.chassisSerialNumber[:], info.ChassisSerialNumber[:])
		fillUchars(platformInfo.ibGuid[:], nil)
		platformInfo.slotNumber = C.uchar(info.SlotNumber)
		platformInfo.trayIndex = C.uchar(info.TrayIndex)
		platformInfo.hostId = C.uchar(info.HostID)
		platformInfo.peerType = C.uchar(info.PeerType)
		platformInfo.moduleId = C.uchar(info.ModuleID)
		return C.NVML_SUCCESS
	default:
		debugLog("[NVML] nvmlDeviceGetPlatformInfo rejected version 0x%x (v1=0x%x v2=0x%x)\n",
			requested, v1Tag, v2Tag)
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
}

//export nvmlDeviceGetModuleId
func nvmlDeviceGetModuleId(device C.nvmlDevice_t, moduleId *C.uint) C.nvmlReturn_t {
	if moduleId == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev, ok := lookupPlatformDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	id, ret := dev.GetMockModuleID()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*moduleId = C.uint(id)
	return C.NVML_SUCCESS
}

// fillUchars writes src over the whole of the caller's buffer, zeroing whatever
// src does not reach. Every byte is written on purpose: the struct arrives
// uninitialised, so a partial write would render caller garbage — and for the
// ibGuid, which the mock does not model, zero is the whole payload. copy() is
// not usable here because cgo's C.uchar is a distinct type from byte.
func fillUchars(dst []C.uchar, src []byte) {
	for i := range dst {
		var b byte
		if i < len(src) {
			b = src[i]
		}
		dst[i] = C.uchar(b)
	}
}

func lookupPlatformDevice(device C.nvmlDevice_t) (*engine.ConfigurableDevice, bool) {
	handle := unsafe.Pointer(device.handle)
	dev, ok := engine.GetEngine().LookupDevice(handle).(*engine.ConfigurableDevice)
	return dev, ok
}
