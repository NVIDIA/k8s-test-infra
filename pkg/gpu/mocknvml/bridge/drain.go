// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// The drain, hot-plug and excluded-GPU exports behind `nvidia-smi drain`.
// The calls take a PCI address rather than a device handle, so the bridge
// reads the caller's nvmlPciInfo_t in place as go-nvml's PciInfo; the two
// layouts are pinned by TestDrainLayouts_MatchGoNvml.
package main

/*
#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func goPciInfo(pciInfo *C.nvmlPciInfo_t) *nvml.PciInfo {
	return (*nvml.PciInfo)(unsafe.Pointer(pciInfo))
}

//export nvmlDeviceQueryDrainState
func nvmlDeviceQueryDrainState(pciInfo *C.nvmlPciInfo_t, currentState *C.nvmlEnableState_t) C.nvmlReturn_t {
	if currentState == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	state, ret := engine.GetEngine().QueryDrainState(goPciInfo(pciInfo))
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*currentState = C.nvmlEnableState_t(state)
	return C.NVML_SUCCESS
}

//export nvmlDeviceModifyDrainState
func nvmlDeviceModifyDrainState(pciInfo *C.nvmlPciInfo_t, newState C.nvmlEnableState_t) C.nvmlReturn_t {
	return toReturn(engine.GetEngine().ModifyDrainState(goPciInfo(pciInfo), nvml.EnableState(newState)))
}

//export nvmlDeviceRemoveGpu_v2
func nvmlDeviceRemoveGpu_v2(
	pciInfo *C.nvmlPciInfo_t, gpuState C.nvmlDetachGpuState_t, linkState C.nvmlPcieLinkState_t,
) C.nvmlReturn_t {
	return toReturn(engine.GetEngine().RemoveGpu(
		goPciInfo(pciInfo), nvml.DetachGpuState(gpuState), nvml.PcieLinkState(linkState)))
}

// nvmlDeviceRemoveGpu is the pre-versioning ABI, which go-nvml's RemoveGpu_v1
// resolves. It predates the gpuState and linkState arguments and leaves both
// the PCI device and its link in place.
//
//export nvmlDeviceRemoveGpu
func nvmlDeviceRemoveGpu(pciInfo *C.nvmlPciInfo_t) C.nvmlReturn_t {
	return nvmlDeviceRemoveGpu_v2(pciInfo,
		C.nvmlDetachGpuState_t(nvml.DETACH_GPU_KEEP), C.nvmlPcieLinkState_t(nvml.PCIE_LINK_KEEP))
}

//export nvmlDeviceDiscoverGpus
func nvmlDeviceDiscoverGpus(pciInfo *C.nvmlPciInfo_t) C.nvmlReturn_t {
	return toReturn(engine.GetEngine().DiscoverGpus(goPciInfo(pciInfo)))
}

//export nvmlGetExcludedDeviceCount
func nvmlGetExcludedDeviceCount(deviceCount *C.uint) C.nvmlReturn_t {
	if deviceCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	excluded, ret := engine.GetEngine().ExcludedDevices()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*deviceCount = C.uint(len(excluded))
	return C.NVML_SUCCESS
}

//export nvmlGetExcludedDeviceInfoByIndex
func nvmlGetExcludedDeviceInfoByIndex(index C.uint, info *C.nvmlExcludedDeviceInfo_t) C.nvmlReturn_t {
	if info == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	excluded, ret := engine.GetEngine().ExcludedDevices()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	if int(index) >= len(excluded) {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	*(*nvml.ExcludedDeviceInfo)(unsafe.Pointer(info)) = excluded[index]
	return C.NVML_SUCCESS
}
