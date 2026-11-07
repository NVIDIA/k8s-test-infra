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

// Package main provides the NVML memory repair, InfoROM and ECC setter
// functions that nv-sentinel-class health agents drive a remediation loop
// with. The engine answers every one of them from the same ECC and row-remap
// state, so an injected fault reads the same through each follow-up query the
// loop makes. See issue NVIDIA/k8s-test-infra#1040.
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

// structVersionV1 is the tag a caller must stamp into a versioned struct of the
// given size. Requiring an exact match keeps the writes below inside the
// caller's allocation, as for the SRAM status.
func structVersionV1(size uintptr) uint32 {
	return FabricStructVersion(size, 1)
}

func repairStatusVersion() uint32 { return structVersionV1(unsafe.Sizeof(C.nvmlRepairStatus_t{})) }

func dramEncryptionVersion() uint32 {
	return structVersionV1(unsafe.Sizeof(C.nvmlDramEncryptionInfo_t{}))
}

func sramUniqueCountsVersion() uint32 {
	return structVersionV1(unsafe.Sizeof(C.nvmlEccSramUniqueUncorrectedErrorCounts_t{}))
}

func lookupECCDevice(device C.nvmlDevice_t) *engine.ConfigurableDevice {
	return engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
}

//export nvmlDeviceGetRepairStatus
func nvmlDeviceGetRepairStatus(device C.nvmlDevice_t, repairStatus *C.nvmlRepairStatus_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetRepairStatus"); !ok {
		return ret
	}
	if repairStatus == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(repairStatus.version) != repairStatusVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	status, ret := dev.GetRepairStatus()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	repairStatus.bChannelRepairPending = C.uint(status.BChannelRepairPending)
	repairStatus.bTpcRepairPending = C.uint(status.BTpcRepairPending)
	return C.NVML_SUCCESS
}

// nvmlUnrepairableMemoryStatus_v1_t has no version field, so there is nothing
// to check before writing it.
//
//export nvmlDeviceGetUnrepairableMemoryFlag_v1
func nvmlDeviceGetUnrepairableMemoryFlag_v1(device C.nvmlDevice_t, unrepairableMemoryStatus *C.nvmlUnrepairableMemoryStatus_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetUnrepairableMemoryFlag_v1"); !ok {
		return ret
	}
	if unrepairableMemoryStatus == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	status, ret := dev.GetUnrepairableMemoryFlag()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	unrepairableMemoryStatus.bUnrepairableMemory = C.uint(status.BUnrepairableMemory)
	return C.NVML_SUCCESS
}

// The entries buffer is the caller's, and the C and Go entry layouts are
// identical (pinned by ecc_repair_layout_test.go), so the engine fills it in
// place rather than the bridge copying field by field.
//
//export nvmlDeviceGetSramUniqueUncorrectedEccErrorCounts
func nvmlDeviceGetSramUniqueUncorrectedEccErrorCounts(device C.nvmlDevice_t, errorCounts *C.nvmlEccSramUniqueUncorrectedErrorCounts_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetSramUniqueUncorrectedEccErrorCounts"); !ok {
		return ret
	}
	if errorCounts == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(errorCounts.version) != sramUniqueCountsVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	counts := nvml.EccSramUniqueUncorrectedErrorCounts{
		Version:    uint32(errorCounts.version),
		EntryCount: uint32(errorCounts.entryCount),
		Entries:    (*nvml.EccSramUniqueUncorrectedErrorEntry_v1)(unsafe.Pointer(errorCounts.entries)),
	}
	ret := dev.GetSramUniqueUncorrectedEccErrorCounts(&counts)
	if ret == nvml.SUCCESS || ret == nvml.ERROR_INSUFFICIENT_SIZE {
		errorCounts.entryCount = C.uint(counts.EntryCount)
	}
	return toReturn(ret)
}

//export nvmlDeviceGetDramEncryptionMode
func nvmlDeviceGetDramEncryptionMode(device C.nvmlDevice_t, current *C.nvmlDramEncryptionInfo_t, pending *C.nvmlDramEncryptionInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetDramEncryptionMode"); !ok {
		return ret
	}
	if current == nil || pending == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(current.version) != dramEncryptionVersion() || uint32(pending.version) != dramEncryptionVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	cur, pend, ret := dev.GetDramEncryptionMode()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	current.encryptionState = C.nvmlEnableState_t(cur.EncryptionState)
	pending.encryptionState = C.nvmlEnableState_t(pend.EncryptionState)
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetDramEncryptionMode
func nvmlDeviceSetDramEncryptionMode(device C.nvmlDevice_t, dramEncryption *C.nvmlDramEncryptionInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetDramEncryptionMode"); !ok {
		return ret
	}
	if dramEncryption == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if uint32(dramEncryption.version) != dramEncryptionVersion() {
		return C.NVML_ERROR_ARGUMENT_VERSION_MISMATCH
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetDramEncryptionMode(&nvml.DramEncryptionInfo{
		Version:         uint32(dramEncryption.version),
		EncryptionState: uint32(dramEncryption.encryptionState),
	}))
}

//export nvmlDeviceValidateInforom
func nvmlDeviceValidateInforom(device C.nvmlDevice_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceValidateInforom"); !ok {
		return ret
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.ValidateInforom())
}

//export nvmlDeviceGetInforomConfigurationChecksum
func nvmlDeviceGetInforomConfigurationChecksum(device C.nvmlDevice_t, checksum *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetInforomConfigurationChecksum"); !ok {
		return ret
	}
	if checksum == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	sum, ret := dev.GetInforomConfigurationChecksum()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	*checksum = C.uint(sum)
	return C.NVML_SUCCESS
}

//export nvmlDeviceSetEccMode
func nvmlDeviceSetEccMode(device C.nvmlDevice_t, ecc C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceSetEccMode"); !ok {
		return ret
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.SetEccMode(nvml.EnableState(ecc)))
}

//export nvmlDeviceClearEccErrorCounts
func nvmlDeviceClearEccErrorCounts(device C.nvmlDevice_t, counterType C.nvmlEccCounterType_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceClearEccErrorCounts"); !ok {
		return ret
	}
	dev := lookupECCDevice(device)
	if dev == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	return toReturn(dev.ClearEccErrorCounts(nvml.EccCounterType(counterType)))
}
