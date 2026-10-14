// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

/*
#include <stdlib.h>
#include <string.h>

#include "nvml_types.h"
*/
import "C"

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// vGPU exports. The engine owns the model; these marshal it. vGPU types and
// instances are plain IDs, resolved through the engine rather than the handle
// table.
//
// Left to the generated stubs on purpose, rendering N/A in nvidia-smi:
// placement, mdev UUID, accounting PIDs, utilization, BAR1 and capability
// queries, heterogeneous mode, and GpuPciId — the guest-side PCI address only
// a running guest driver reports. GridLicensableFeatures v1–v3 have older
// layouts and no current caller.

func lookupVgpuDevice(device C.nvmlDevice_t) (*engine.ConfigurableDevice, C.nvmlReturn_t) {
	dev := engine.GetEngine().LookupConfigurableDevice(unsafe.Pointer(device.handle))
	if dev == nil {
		return nil, C.NVML_ERROR_INVALID_ARGUMENT
	}
	return dev, C.NVML_SUCCESS
}

func lookupVgpuType(id C.nvmlVgpuTypeId_t) (engine.VgpuType, C.nvmlReturn_t) {
	t, ret := engine.GetEngine().VgpuType(uint32(id))
	return t, toReturn(ret)
}

func lookupVgpuInstance(id C.nvmlVgpuInstance_t) (engine.VgpuInstance, C.nvmlReturn_t) {
	inst, ret := engine.GetEngine().VgpuInstance(uint32(id))
	return inst, toReturn(ret)
}

// copyIDs applies NVML's two-call array contract: the caller passes its
// capacity in *count and always gets the required length back, and the IDs
// are written only when they all fit.
func copyIDs[T ~uint32](count *uint32, out *T, ids []uint32) nvml.Return {
	if count == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	capacity := *count
	*count = uint32(len(ids)) //nolint:gosec // bounded by maxVgpuInstancesPerGPU (999) or a validated type list
	if len(ids) == 0 {
		return nvml.SUCCESS
	}
	if out == nil || capacity < uint32(len(ids)) { //nolint:gosec // bounded as above
		return nvml.ERROR_INSUFFICIENT_SIZE
	}
	dst := unsafe.Slice(out, len(ids))
	for i, id := range ids {
		dst[i] = T(id)
	}
	return nvml.SUCCESS
}

// copySizedString is goStringToC for the getters whose size is in/out: a short
// buffer gets the required size back with INSUFFICIENT_SIZE.
func copySizedString(s string, buf *C.char, size *C.uint) C.nvmlReturn_t {
	if size == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	need := C.uint(len(s) + 1)
	if buf == nil || *size < need {
		*size = need
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	return goStringToC(s, buf, *size)
}

func deviceIDList(funcName string, device C.nvmlDevice_t, count *C.uint, get func(*engine.ConfigurableDevice) ([]uint32, nvml.Return)) ([]uint32, C.nvmlReturn_t) {
	if ret, ok := bridgeVersionCheck(funcName); !ok {
		return nil, ret
	}
	if count == nil {
		return nil, C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev, ret := lookupVgpuDevice(device)
	if ret != C.NVML_SUCCESS {
		return nil, ret
	}
	ids, r := get(dev)
	return ids, toReturn(r)
}

//export nvmlDeviceGetHostVgpuMode
func nvmlDeviceGetHostVgpuMode(device C.nvmlDevice_t, pHostVgpuMode *C.nvmlHostVgpuMode_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetHostVgpuMode"); !ok {
		return ret
	}
	if pHostVgpuMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev, ret := lookupVgpuDevice(device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	mode, r := dev.GetHostVgpuMode()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*pHostVgpuMode = C.nvmlHostVgpuMode_t(mode)
	return C.NVML_SUCCESS
}

//export nvmlDeviceGetSupportedVgpus
func nvmlDeviceGetSupportedVgpus(device C.nvmlDevice_t, vgpuCount *C.uint, vgpuTypeIds *C.nvmlVgpuTypeId_t) C.nvmlReturn_t {
	ids, ret := deviceIDList("nvmlDeviceGetSupportedVgpus", device, vgpuCount, (*engine.ConfigurableDevice).VgpuSupportedTypeIDs)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(copyIDs((*uint32)(unsafe.Pointer(vgpuCount)), vgpuTypeIds, ids))
}

//export nvmlDeviceGetCreatableVgpus
func nvmlDeviceGetCreatableVgpus(device C.nvmlDevice_t, vgpuCount *C.uint, vgpuTypeIds *C.nvmlVgpuTypeId_t) C.nvmlReturn_t {
	ids, ret := deviceIDList("nvmlDeviceGetCreatableVgpus", device, vgpuCount, (*engine.ConfigurableDevice).VgpuCreatableTypeIDs)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(copyIDs((*uint32)(unsafe.Pointer(vgpuCount)), vgpuTypeIds, ids))
}

//export nvmlDeviceGetActiveVgpus
func nvmlDeviceGetActiveVgpus(device C.nvmlDevice_t, vgpuCount *C.uint, vgpuInstances *C.nvmlVgpuInstance_t) C.nvmlReturn_t {
	ids, ret := deviceIDList("nvmlDeviceGetActiveVgpus", device, vgpuCount, (*engine.ConfigurableDevice).VgpuActiveInstanceIDs)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return toReturn(copyIDs((*uint32)(unsafe.Pointer(vgpuCount)), vgpuInstances, ids))
}

//export nvmlDeviceGetGridLicensableFeatures_v4
func nvmlDeviceGetGridLicensableFeatures_v4(device C.nvmlDevice_t, pGridLicensableFeatures *C.nvmlGridLicensableFeatures_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlDeviceGetGridLicensableFeatures_v4"); !ok {
		return ret
	}
	if pGridLicensableFeatures == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev, ret := lookupVgpuDevice(device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	features, r := dev.GetGridLicensableFeatures()
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	// Same layout, pinned by TestVgpuLicenseLayouts_MatchGoNvml.
	*(*nvml.GridLicensableFeatures)(unsafe.Pointer(pGridLicensableFeatures)) = features
	return C.NVML_SUCCESS
}

//export nvmlVgpuTypeGetMaxInstances
func nvmlVgpuTypeGetMaxInstances(device C.nvmlDevice_t, vgpuTypeId C.nvmlVgpuTypeId_t, vgpuInstanceCount *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetMaxInstances"); !ok {
		return ret
	}
	if vgpuInstanceCount == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	dev, ret := lookupVgpuDevice(device)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	t, r := dev.VgpuType(uint32(vgpuTypeId))
	if r != nvml.SUCCESS {
		return toReturn(r)
	}
	*vgpuInstanceCount = C.uint(t.MaxInstances)
	return C.NVML_SUCCESS
}

//export nvmlVgpuTypeGetName
func nvmlVgpuTypeGetName(vgpuTypeId C.nvmlVgpuTypeId_t, vgpuTypeName *C.char, size *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetName"); !ok {
		return ret
	}
	t, ret := lookupVgpuType(vgpuTypeId)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return copySizedString(t.Name, vgpuTypeName, size)
}

//export nvmlVgpuTypeGetClass
func nvmlVgpuTypeGetClass(vgpuTypeId C.nvmlVgpuTypeId_t, vgpuTypeClass *C.char, size *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetClass"); !ok {
		return ret
	}
	t, ret := lookupVgpuType(vgpuTypeId)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return copySizedString(t.Class, vgpuTypeClass, size)
}

//export nvmlVgpuTypeGetLicense
func nvmlVgpuTypeGetLicense(vgpuTypeId C.nvmlVgpuTypeId_t, vgpuTypeLicenseString *C.char, size C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetLicense"); !ok {
		return ret
	}
	t, ret := lookupVgpuType(vgpuTypeId)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return goStringToC(t.License, vgpuTypeLicenseString, size)
}

//export nvmlVgpuTypeGetFramebufferSize
func nvmlVgpuTypeGetFramebufferSize(vgpuTypeId C.nvmlVgpuTypeId_t, fbSize *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetFramebufferSize"); !ok {
		return ret
	}
	if fbSize == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	t, ret := lookupVgpuType(vgpuTypeId)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	*fbSize = C.ulonglong(t.FramebufferBytes)
	return C.NVML_SUCCESS
}

//export nvmlVgpuTypeGetDeviceID
func nvmlVgpuTypeGetDeviceID(vgpuTypeId C.nvmlVgpuTypeId_t, deviceID *C.ulonglong, subsystemID *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetDeviceID"); !ok {
		return ret
	}
	if deviceID == nil || subsystemID == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	t, ret := lookupVgpuType(vgpuTypeId)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	*deviceID, *subsystemID = C.ulonglong(t.DeviceID), C.ulonglong(t.SubsystemID)
	return C.NVML_SUCCESS
}

// vgpuTypeUint serves the type getters that write one unsigned int.
func vgpuTypeUint(funcName string, id C.nvmlVgpuTypeId_t, out *C.uint, field func(engine.VgpuType) uint32) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck(funcName); !ok {
		return ret
	}
	if out == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	t, ret := lookupVgpuType(id)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	*out = C.uint(field(t))
	return C.NVML_SUCCESS
}

//export nvmlVgpuTypeGetGpuInstanceProfileId
func nvmlVgpuTypeGetGpuInstanceProfileId(vgpuTypeId C.nvmlVgpuTypeId_t, gpuInstanceProfileId *C.uint) C.nvmlReturn_t {
	return vgpuTypeUint("nvmlVgpuTypeGetGpuInstanceProfileId", vgpuTypeId, gpuInstanceProfileId,
		func(t engine.VgpuType) uint32 { return t.GPUInstanceProfileID })
}

//export nvmlVgpuTypeGetNumDisplayHeads
func nvmlVgpuTypeGetNumDisplayHeads(vgpuTypeId C.nvmlVgpuTypeId_t, numDisplayHeads *C.uint) C.nvmlReturn_t {
	return vgpuTypeUint("nvmlVgpuTypeGetNumDisplayHeads", vgpuTypeId, numDisplayHeads,
		func(t engine.VgpuType) uint32 { return t.NumDisplayHeads })
}

//export nvmlVgpuTypeGetFrameRateLimit
func nvmlVgpuTypeGetFrameRateLimit(vgpuTypeId C.nvmlVgpuTypeId_t, frameRateLimit *C.uint) C.nvmlReturn_t {
	return vgpuTypeUint("nvmlVgpuTypeGetFrameRateLimit", vgpuTypeId, frameRateLimit,
		func(t engine.VgpuType) uint32 { return t.FrameRateLimit })
}

//export nvmlVgpuTypeGetMaxInstancesPerVm
func nvmlVgpuTypeGetMaxInstancesPerVm(vgpuTypeId C.nvmlVgpuTypeId_t, vgpuInstanceCountPerVm *C.uint) C.nvmlReturn_t {
	return vgpuTypeUint("nvmlVgpuTypeGetMaxInstancesPerVm", vgpuTypeId, vgpuInstanceCountPerVm,
		func(t engine.VgpuType) uint32 { return t.MaxInstancesPerVM })
}

//export nvmlVgpuTypeGetResolution
func nvmlVgpuTypeGetResolution(vgpuTypeId C.nvmlVgpuTypeId_t, displayIndex C.uint, xdim *C.uint, ydim *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuTypeGetResolution"); !ok {
		return ret
	}
	if xdim == nil || ydim == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	t, ret := lookupVgpuType(vgpuTypeId)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if uint32(displayIndex) >= t.NumDisplayHeads {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	*xdim, *ydim = C.uint(t.ResolutionWidth), C.uint(t.ResolutionHeight)
	return C.NVML_SUCCESS
}

//export nvmlVgpuInstanceGetUUID
func nvmlVgpuInstanceGetUUID(vgpuInstance C.nvmlVgpuInstance_t, uuid *C.char, size C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetUUID"); !ok {
		return ret
	}
	inst, ret := lookupVgpuInstance(vgpuInstance)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return goStringToC(inst.UUID, uuid, size)
}

//export nvmlVgpuInstanceGetVmID
func nvmlVgpuInstanceGetVmID(vgpuInstance C.nvmlVgpuInstance_t, vmId *C.char, size C.uint, vmIdType *C.nvmlVgpuVmIdType_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetVmID"); !ok {
		return ret
	}
	if vmIdType == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	inst, ret := lookupVgpuInstance(vgpuInstance)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	if r := goStringToC(inst.VMID, vmId, size); r != C.NVML_SUCCESS {
		return r
	}
	*vmIdType = C.NVML_VGPU_VM_ID_UUID
	return C.NVML_SUCCESS
}

//export nvmlVgpuInstanceGetVmDriverVersion
func nvmlVgpuInstanceGetVmDriverVersion(vgpuInstance C.nvmlVgpuInstance_t, version *C.char, length C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetVmDriverVersion"); !ok {
		return ret
	}
	inst, ret := lookupVgpuInstance(vgpuInstance)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	return goStringToC(inst.GuestDriverVersion, version, length)
}

//export nvmlVgpuInstanceGetType
func nvmlVgpuInstanceGetType(vgpuInstance C.nvmlVgpuInstance_t, vgpuTypeId *C.nvmlVgpuTypeId_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetType"); !ok {
		return ret
	}
	if vgpuTypeId == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	inst, ret := lookupVgpuInstance(vgpuInstance)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	*vgpuTypeId = C.nvmlVgpuTypeId_t(inst.Type.ID)
	return C.NVML_SUCCESS
}

// vgpuInstanceUint serves the instance getters that write one unsigned int.
func vgpuInstanceUint(funcName string, id C.nvmlVgpuInstance_t, out *C.uint, field func(engine.VgpuInstance) uint32) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck(funcName); !ok {
		return ret
	}
	if out == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	inst, ret := lookupVgpuInstance(id)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	*out = C.uint(field(inst))
	return C.NVML_SUCCESS
}

// nvidia-smi drops an instance's row from `nvidia-smi vgpu` when this fails,
// so a time-sliced instance answers "no GPU instance" rather than an error.
//
//export nvmlVgpuInstanceGetGpuInstanceId
func nvmlVgpuInstanceGetGpuInstanceId(vgpuInstance C.nvmlVgpuInstance_t, gpuInstanceId *C.uint) C.nvmlReturn_t {
	return vgpuInstanceUint("nvmlVgpuInstanceGetGpuInstanceId", vgpuInstance, gpuInstanceId,
		func(i engine.VgpuInstance) uint32 { return i.GPUInstanceID })
}

//export nvmlVgpuInstanceGetFrameRateLimit
func nvmlVgpuInstanceGetFrameRateLimit(vgpuInstance C.nvmlVgpuInstance_t, frameRateLimit *C.uint) C.nvmlReturn_t {
	return vgpuInstanceUint("nvmlVgpuInstanceGetFrameRateLimit", vgpuInstance, frameRateLimit,
		func(i engine.VgpuInstance) uint32 { return i.Type.FrameRateLimit })
}

//export nvmlVgpuInstanceGetEccMode
func nvmlVgpuInstanceGetEccMode(vgpuInstance C.nvmlVgpuInstance_t, eccMode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetEccMode"); !ok {
		return ret
	}
	if eccMode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	inst, ret := lookupVgpuInstance(vgpuInstance)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	*eccMode = C.nvmlEnableState_t(inst.ECCMode)
	return C.NVML_SUCCESS
}

// No process runs in a simulated guest, so the instance's accounting is off
// and its framebuffer, encoder and FBC counters are idle.
//
//export nvmlVgpuInstanceGetAccountingMode
func nvmlVgpuInstanceGetAccountingMode(vgpuInstance C.nvmlVgpuInstance_t, mode *C.nvmlEnableState_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetAccountingMode"); !ok {
		return ret
	}
	if mode == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if _, ret := lookupVgpuInstance(vgpuInstance); ret != C.NVML_SUCCESS {
		return ret
	}
	*mode = C.nvmlEnableState_t(nvml.FEATURE_DISABLED)
	return C.NVML_SUCCESS
}

//export nvmlVgpuInstanceGetFbUsage
func nvmlVgpuInstanceGetFbUsage(vgpuInstance C.nvmlVgpuInstance_t, fbUsage *C.ulonglong) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetFbUsage"); !ok {
		return ret
	}
	if fbUsage == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if _, ret := lookupVgpuInstance(vgpuInstance); ret != C.NVML_SUCCESS {
		return ret
	}
	*fbUsage = 0
	return C.NVML_SUCCESS
}

//export nvmlVgpuInstanceGetEncoderStats
func nvmlVgpuInstanceGetEncoderStats(vgpuInstance C.nvmlVgpuInstance_t, sessionCount *C.uint, averageFps *C.uint, averageLatency *C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetEncoderStats"); !ok {
		return ret
	}
	if sessionCount == nil || averageFps == nil || averageLatency == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if _, ret := lookupVgpuInstance(vgpuInstance); ret != C.NVML_SUCCESS {
		return ret
	}
	*sessionCount, *averageFps, *averageLatency = 0, 0, 0
	return C.NVML_SUCCESS
}

//export nvmlVgpuInstanceGetFBCStats
func nvmlVgpuInstanceGetFBCStats(vgpuInstance C.nvmlVgpuInstance_t, fbcStats *C.nvmlFBCStats_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetFBCStats"); !ok {
		return ret
	}
	if fbcStats == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if _, ret := lookupVgpuInstance(vgpuInstance); ret != C.NVML_SUCCESS {
		return ret
	}
	*fbcStats = C.nvmlFBCStats_t{}
	return C.NVML_SUCCESS
}

//export nvmlVgpuInstanceGetLicenseInfo_v2
func nvmlVgpuInstanceGetLicenseInfo_v2(vgpuInstance C.nvmlVgpuInstance_t, licenseInfo *C.nvmlVgpuLicenseInfo_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlVgpuInstanceGetLicenseInfo_v2"); !ok {
		return ret
	}
	if licenseInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	inst, ret := lookupVgpuInstance(vgpuInstance)
	if ret != C.NVML_SUCCESS {
		return ret
	}
	// Same layout, pinned by TestVgpuLicenseLayouts_MatchGoNvml.
	*(*nvml.VgpuLicenseInfo)(unsafe.Pointer(licenseInfo)) = inst.LicenseInfo()
	return C.NVML_SUCCESS
}
