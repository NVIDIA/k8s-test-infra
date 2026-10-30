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

// Package main provides the NVML Confidential Compute (CC) functions: the
// node-wide capabilities, state, settings, GPUs ready state and key rotation
// threshold, and per GPU the memory split, attestation report and certificate.
//
// nvidia-smi calls the two memory getters once per GPU on every -q run: the
// protected memory usage backs the "Conf Compute Protected Memory Usage" block,
// and the memory size info backs the protected/unprotected split behind it.
// While CC mode is off — every shipped profile — the engine answers zero rather
// than N/A, which is what real NVML reports. See issue NVIDIA/k8s-test-infra#711.
// `nvidia-smi conf-compute` drives the rest.
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

//export nvmlDeviceGetConfComputeMemSizeInfo
func nvmlDeviceGetConfComputeMemSizeInfo(device C.nvmlDevice_t, memInfo *C.nvmlConfComputeMemSizeInfo_t) C.nvmlReturn_t {
	if memInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetConfComputeMemSizeInfo"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetConfComputeMemSizeInfo()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	memInfo.protectedMemSizeKib = C.ulonglong(info.ProtectedMemSizeKib)
	memInfo.unprotectedMemSizeKib = C.ulonglong(info.UnprotectedMemSizeKib)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetConfComputeProtectedMemoryUsage
func nvmlDeviceGetConfComputeProtectedMemoryUsage(device C.nvmlDevice_t, memory *C.nvmlMemory_t) C.nvmlReturn_t {
	if memory == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetConfComputeProtectedMemoryUsage"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	mem, ret := dev.GetConfComputeProtectedMemoryUsage()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	memory.total = C.ulonglong(mem.Total)
	memory.free = C.ulonglong(mem.Free)
	memory.used = C.ulonglong(mem.Used)
	return C.NVML_SUCCESS
}

func lookupConfComputeDevice(device C.nvmlDevice_t) (*engine.ConfigurableDevice, bool) {
	handle := unsafe.Pointer(device.handle)
	dev, ok := engine.GetEngine().LookupDevice(handle).(*engine.ConfigurableDevice)
	return dev, ok
}

// The version tags of the three CC structs that carry one. Each has a single
// upstream layout, so v1 is the only tag accepted.
func confComputeSettingsVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlSystemConfComputeSettings_t{}), 1)
}

func confComputeGetKeyRotationVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlConfComputeGetKeyRotationThresholdInfo_t{}), 1)
}

func confComputeSetKeyRotationVersion() uint32 {
	return FabricStructVersion(unsafe.Sizeof(C.nvmlConfComputeSetKeyRotationThresholdInfo_t{}), 1)
}

// confComputeStructVersionOK reports whether the caller's version field is the
// one layout that exists, logging the rejection. It is pure Go so it can be
// tested without cgo, which test files in this package cannot use.
func confComputeStructVersionOK(funcName string, requested, v1 uint32) bool {
	if requested == v1 {
		return true
	}
	debugLog("[NVML] %s rejected struct version 0x%x (v1=0x%x)\n", funcName, requested, v1)
	return false
}

//export nvmlSystemGetConfComputeCapabilities
func nvmlSystemGetConfComputeCapabilities(capabilities *C.nvmlConfComputeSystemCaps_t) C.nvmlReturn_t {
	if capabilities == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemGetConfComputeCapabilities"); !ok {
		return ret
	}
	caps, ret := engine.GetEngine().SystemGetConfComputeCapabilities()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	capabilities.cpuCaps = C.uint(caps.CpuCaps)
	capabilities.gpusCaps = C.uint(caps.GpusCaps)
	return C.NVML_SUCCESS
}

//export nvmlSystemGetConfComputeState
func nvmlSystemGetConfComputeState(state *C.nvmlConfComputeSystemState_t) C.nvmlReturn_t {
	if state == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemGetConfComputeState"); !ok {
		return ret
	}
	s, ret := engine.GetEngine().SystemGetConfComputeState()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	state.environment = C.uint(s.Environment)
	state.ccFeature = C.uint(s.CcFeature)
	state.devToolsMode = C.uint(s.DevToolsMode)
	return C.NVML_SUCCESS
}

//export nvmlSystemGetConfComputeSettings
func nvmlSystemGetConfComputeSettings(settings *C.nvmlSystemConfComputeSettings_t) C.nvmlReturn_t {
	if settings == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemGetConfComputeSettings"); !ok {
		return ret
	}
	if !confComputeStructVersionOK("nvmlSystemGetConfComputeSettings",
		uint32(settings.version), confComputeSettingsVersion()) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	s, ret := engine.GetEngine().SystemGetConfComputeSettings()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	settings.environment = C.uint(s.Environment)
	settings.ccFeature = C.uint(s.CcFeature)
	settings.devToolsMode = C.uint(s.DevToolsMode)
	settings.multiGpuMode = C.uint(s.MultiGpuMode)
	return C.NVML_SUCCESS
}

//export nvmlSystemGetConfComputeGpusReadyState
func nvmlSystemGetConfComputeGpusReadyState(isAcceptingWork *C.uint) C.nvmlReturn_t {
	if isAcceptingWork == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemGetConfComputeGpusReadyState"); !ok {
		return ret
	}
	ready, ret := engine.GetEngine().SystemGetConfComputeGpusReadyState()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*isAcceptingWork = C.uint(ready)
	return C.NVML_SUCCESS
}

//export nvmlSystemSetConfComputeGpusReadyState
func nvmlSystemSetConfComputeGpusReadyState(isAcceptingWork C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemSetConfComputeGpusReadyState"); !ok {
		return ret
	}
	return toReturn(engine.GetEngine().SystemSetConfComputeGpusReadyState(uint32(isAcceptingWork)))
}

//export nvmlSystemGetConfComputeKeyRotationThresholdInfo
func nvmlSystemGetConfComputeKeyRotationThresholdInfo(pKeyRotationThrInfo *C.nvmlConfComputeGetKeyRotationThresholdInfo_t) C.nvmlReturn_t {
	if pKeyRotationThrInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemGetConfComputeKeyRotationThresholdInfo"); !ok {
		return ret
	}
	if !confComputeStructVersionOK("nvmlSystemGetConfComputeKeyRotationThresholdInfo",
		uint32(pKeyRotationThrInfo.version), confComputeGetKeyRotationVersion()) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	advantage, ret := engine.GetEngine().SystemGetConfComputeKeyRotationThreshold()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	pKeyRotationThrInfo.attackerAdvantage = C.ulonglong(advantage)
	return C.NVML_SUCCESS
}

//export nvmlSystemSetConfComputeKeyRotationThresholdInfo
func nvmlSystemSetConfComputeKeyRotationThresholdInfo(pKeyRotationThrInfo *C.nvmlConfComputeSetKeyRotationThresholdInfo_t) C.nvmlReturn_t {
	if pKeyRotationThrInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlSystemSetConfComputeKeyRotationThresholdInfo"); !ok {
		return ret
	}
	if !confComputeStructVersionOK("nvmlSystemSetConfComputeKeyRotationThresholdInfo",
		uint32(pKeyRotationThrInfo.version), confComputeSetKeyRotationVersion()) {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	return toReturn(engine.GetEngine().SystemSetConfComputeKeyRotationThreshold(
		uint64(pKeyRotationThrInfo.maxAttackerAdvantage)))
}

//export nvmlDeviceGetConfComputeGpuAttestationReport
func nvmlDeviceGetConfComputeGpuAttestationReport(device C.nvmlDevice_t, gpuAtstReport *C.nvmlConfComputeGpuAttestationReport_t) C.nvmlReturn_t {
	if gpuAtstReport == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetConfComputeGpuAttestationReport"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// The nonce is the one input field; read it before the struct is
	// overwritten.
	var nonce [32]byte
	copy(nonce[:], C.GoBytes(unsafe.Pointer(&gpuAtstReport.nonce[0]), C.int(len(nonce))))
	report, ret := dev.GetMockConfComputeGpuAttestationReport(nonce)
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// Writing the whole struct covers every byte, including the tails past
	// the reported sizes, so no caller garbage leaks through.
	*(*nvml.ConfComputeGpuAttestationReport)(unsafe.Pointer(gpuAtstReport)) = report
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetConfComputeGpuCertificate
func nvmlDeviceGetConfComputeGpuCertificate(device C.nvmlDevice_t, gpuCert *C.nvmlConfComputeGpuCertificate_t) C.nvmlReturn_t {
	if gpuCert == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetConfComputeGpuCertificate"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	cert, ret := dev.GetMockConfComputeGpuCertificate()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*(*nvml.ConfComputeGpuCertificate)(unsafe.Pointer(gpuCert)) = cert
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetConfComputeUnprotectedMemSize
func nvmlDeviceSetConfComputeUnprotectedMemSize(device C.nvmlDevice_t, sizeKiB C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetConfComputeUnprotectedMemSize"); !ok {
		return ret
	}
	dev, ok := lookupConfComputeDevice(device)
	if !ok {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetMockConfComputeUnprotectedMemSize(uint64(sizeKiB)))
}
