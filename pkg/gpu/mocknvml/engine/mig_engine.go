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

package engine

import (
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
)

// This file is the handle-facing half of MIG: it translates between the opaque
// handles the C ABI deals in and the objects in mig.go. The bridge stays free
// of handle-table bookkeeping, and the engine's MIG logic stays free of C.
//
// The engine's own mutex is only read-locked here. The handle tables have
// their own locking, and the MIG state they point at is guarded by migState;
// taking the engine's write lock would serialise every partitioning call
// against every other NVML call for no added safety.

// gpuInstanceOf resolves a GPU instance handle.
func (e *Engine) gpuInstanceOf(handle unsafe.Pointer) (nvml.GpuInstance, nvml.Return) {
	e.mu.RLock()
	initialized := e.initCount > 0
	e.mu.RUnlock()

	if !initialized {
		return nil, nvml.ERROR_UNINITIALIZED
	}
	gi, ok := e.gpuInstances.Lookup(handle)
	if !ok {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	return gi, nvml.SUCCESS
}

// computeInstanceOf resolves a compute instance handle.
func (e *Engine) computeInstanceOf(handle unsafe.Pointer) (nvml.ComputeInstance, nvml.Return) {
	e.mu.RLock()
	initialized := e.initCount > 0
	e.mu.RUnlock()

	if !initialized {
		return nil, nvml.ERROR_UNINITIALIZED
	}
	ci, ok := e.computeInstances.Lookup(handle)
	if !ok {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	return ci, nvml.SUCCESS
}

// registerHandle turns a handle-table registration into an NVML result.
//
// A full table returns nil, and handing that back as a SUCCESS gives the
// caller a handle that fails every subsequent lookup — a silent corruption
// rather than an error it can act on.
func registerHandle[T comparable](table interface{ Register(T) unsafe.Pointer }, item T) (unsafe.Pointer, nvml.Return) {
	handle := table.Register(item)
	if handle == nil {
		return nil, nvml.ERROR_INSUFFICIENT_RESOURCES
	}
	return handle, nvml.SUCCESS
}

// =============================================================================
// GPU instances
// =============================================================================

// DeviceGetGpuInstanceProfileInfo returns a GPU instance profile the board
// supports, for the plain (unversioned) NVML entry point.
func (e *Engine) DeviceGetGpuInstanceProfileInfo(
	handle unsafe.Pointer, profile int,
) (nvml.GpuInstanceProfileInfo, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nvml.GpuInstanceProfileInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	return dev.GetGpuInstanceProfileInfo(profile)
}

// DeviceGetGpuInstanceProfileName returns the name real NVML reports for a GPU
// instance profile, e.g. "MIG 1g.5gb" — the spelling nvidia-smi prints and the
// one the versioned profile-info structs carry.
func (e *Engine) DeviceGetGpuInstanceProfileName(handle unsafe.Pointer, profile int) (string, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return "", nvml.ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetGpuInstanceProfileInfo(profile)
	if ret != nvml.SUCCESS {
		return "", ret
	}
	// A GPU instance profile is named by the compute instance that spans it,
	// which is what makes a 3-slice profile "3g.20gb" and not "1c.3g.20gb".
	ciProfile, err := spanningComputeInstanceProfile(profile)
	if err != nil {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	name, err := migProfileName(profile, ciProfile, info.MemorySizeMB, dev.memoryInfo().Total)
	if err != nil {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return migProfileDisplayName(name), nvml.SUCCESS
}

// GpuInstanceGetComputeInstanceProfileName returns the name real NVML reports
// for a compute instance profile within a GPU instance, e.g. "MIG 1c.3g.20gb".
func (e *Engine) GpuInstanceGetComputeInstanceProfileName(
	handle unsafe.Pointer, profile int,
) (string, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return "", ret
	}
	giInfo, ret := gi.GetInfo()
	if ret != nvml.SUCCESS {
		return "", ret
	}
	dev, ok := giInfo.Device.(*ConfigurableDevice)
	if !ok || dev.migState == nil {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	giProfile, ok := dev.migState.profiles.GpuInstanceProfiles[int(giInfo.ProfileId)]
	if !ok {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	name, err := migProfileName(
		int(giInfo.ProfileId), profile, giProfile.MemorySizeMB, dev.memoryInfo().Total)
	if err != nil {
		return "", nvml.ERROR_NOT_SUPPORTED
	}
	return migProfileDisplayName(name), nvml.SUCCESS
}

// DeviceGetGpuInstancePossiblePlacements returns the slice offsets a profile
// may occupy on this board.
func (e *Engine) DeviceGetGpuInstancePossiblePlacements(
	handle unsafe.Pointer, profile int,
) ([]nvml.GpuInstancePlacement, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetGpuInstanceProfileInfo(profile)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return dev.GetGpuInstancePossiblePlacements(&info)
}

// DeviceGetGpuInstanceRemainingCapacity reports how many more instances of a
// profile still fit.
func (e *Engine) DeviceGetGpuInstanceRemainingCapacity(handle unsafe.Pointer, profile int) (int, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetGpuInstanceProfileInfo(profile)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	return dev.GetGpuInstanceRemainingCapacity(&info)
}

// DeviceCreateGpuInstance partitions the GPU and returns a handle to the new
// instance. placement is optional: nil lets the mock pick the first offset
// that still fits, which is what nvmlDeviceCreateGpuInstance does.
func (e *Engine) DeviceCreateGpuInstance(
	handle unsafe.Pointer, profile int, placement *nvml.GpuInstancePlacement,
) (unsafe.Pointer, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetGpuInstanceProfileInfo(profile)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	var gi nvml.GpuInstance
	if placement == nil {
		gi, ret = dev.CreateGpuInstance(&info)
	} else {
		gi, ret = dev.CreateGpuInstanceWithPlacement(&info, placement)
	}
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return registerHandle(e.gpuInstances, gi)
}

// DeviceGetGpuInstances returns handles for the live instances of a profile.
func (e *Engine) DeviceGetGpuInstances(handle unsafe.Pointer, profile int) ([]unsafe.Pointer, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	info, ret := dev.GetGpuInstanceProfileInfo(profile)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	instances, ret := dev.GetGpuInstances(&info)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	handles := make([]unsafe.Pointer, 0, len(instances))
	for _, gi := range instances {
		handle, ret := registerHandle(e.gpuInstances, gi)
		if ret != nvml.SUCCESS {
			return nil, ret
		}
		handles = append(handles, handle)
	}
	return handles, nvml.SUCCESS
}

// DeviceGetGpuInstanceById returns a handle for the instance with the given ID.
func (e *Engine) DeviceGetGpuInstanceById(handle unsafe.Pointer, id int) (unsafe.Pointer, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	gi, ret := dev.GetGpuInstanceById(id)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return registerHandle(e.gpuInstances, gi)
}

// GpuInstanceGetInfo returns a GPU instance's info together with a handle for
// its parent device, which the C struct carries as a field.
func (e *Engine) GpuInstanceGetInfo(handle unsafe.Pointer) (nvml.GpuInstanceInfo, unsafe.Pointer, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nvml.GpuInstanceInfo{}, nil, ret
	}
	info, ret := gi.GetInfo()
	if ret != nvml.SUCCESS {
		return nvml.GpuInstanceInfo{}, nil, ret
	}
	return info, e.handleForDevice(info.Device), nvml.SUCCESS
}

// GpuInstanceDestroy tears down a GPU instance, invalidating its handle and
// the handles of everything derived from it.
func (e *Engine) GpuInstanceDestroy(handle unsafe.Pointer) nvml.Return {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return ret
	}

	// Collect the descendants before the teardown, because afterwards neither
	// the instance tree nor the parent's MIG device cache names them.
	info, infoRet := gi.GetInfo()
	computeInstances := allComputeInstances(gi)
	var migDevices []*ConfigurableDevice
	if infoRet == nvml.SUCCESS {
		migDevices = migDevicesDerivedFrom(info.Device, info.Id, nil)
	}

	if ret := gi.Destroy(); ret != nvml.SUCCESS {
		return ret
	}

	for _, ci := range computeInstances {
		e.computeInstances.Retire(ci)
	}
	e.retireMigDeviceHandles(migDevices)
	e.gpuInstances.Retire(gi)
	return nvml.SUCCESS
}

// =============================================================================
// Compute instances
// =============================================================================

// GpuInstanceGetComputeInstanceProfileInfo returns a compute instance profile
// available within a GPU instance.
func (e *Engine) GpuInstanceGetComputeInstanceProfileInfo(
	handle unsafe.Pointer, profile, engProfile int,
) (nvml.ComputeInstanceProfileInfo, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nvml.ComputeInstanceProfileInfo{}, ret
	}
	return gi.GetComputeInstanceProfileInfo(profile, engProfile)
}

// GpuInstanceGetComputeInstanceRemainingCapacity reports how many more
// compute instances of a profile still fit in a GPU instance.
func (e *Engine) GpuInstanceGetComputeInstanceRemainingCapacity(
	handle unsafe.Pointer, profile int,
) (int, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	info, ret := gi.GetComputeInstanceProfileInfo(profile, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	return gi.GetComputeInstanceRemainingCapacity(&info)
}

// GpuInstanceGetComputeInstancePossiblePlacements returns the compute-slice
// offsets a profile may occupy within a GPU instance.
func (e *Engine) GpuInstanceGetComputeInstancePossiblePlacements(
	handle unsafe.Pointer, profile int,
) ([]nvml.ComputeInstancePlacement, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	info, ret := gi.GetComputeInstanceProfileInfo(profile, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	mock, ok := gi.(*mockserver.GpuInstance)
	if !ok {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	// Deliberately the same source the create path validates against, so a
	// placement a caller was offered is one it can actually use.
	return computeInstancePlacements(mock, &info)
}

// GpuInstanceCreateComputeInstance partitions a GPU instance further.
// placement is optional, mirroring the two NVML entry points.
func (e *Engine) GpuInstanceCreateComputeInstance(
	handle unsafe.Pointer, profile int, placement *nvml.ComputeInstancePlacement,
) (unsafe.Pointer, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	info, ret := gi.GetComputeInstanceProfileInfo(profile, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	var ci nvml.ComputeInstance
	if placement == nil {
		ci, ret = gi.CreateComputeInstance(&info)
	} else {
		ci, ret = gi.CreateComputeInstanceWithPlacement(&info, placement)
	}
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return registerHandle(e.computeInstances, ci)
}

// GpuInstanceGetComputeInstances returns handles for the live compute
// instances of a profile.
func (e *Engine) GpuInstanceGetComputeInstances(
	handle unsafe.Pointer, profile int,
) ([]unsafe.Pointer, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	info, ret := gi.GetComputeInstanceProfileInfo(profile, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	instances, ret := gi.GetComputeInstances(&info)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	handles := make([]unsafe.Pointer, 0, len(instances))
	for _, ci := range instances {
		handle, ret := registerHandle(e.computeInstances, ci)
		if ret != nvml.SUCCESS {
			return nil, ret
		}
		handles = append(handles, handle)
	}
	return handles, nvml.SUCCESS
}

// GpuInstanceGetComputeInstanceById returns a handle for the compute instance
// with the given ID.
func (e *Engine) GpuInstanceGetComputeInstanceById(handle unsafe.Pointer, id int) (unsafe.Pointer, nvml.Return) {
	gi, ret := e.gpuInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	ci, ret := gi.GetComputeInstanceById(id)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return registerHandle(e.computeInstances, ci)
}

// ComputeInstanceGetInfo returns a compute instance's info together with
// handles for the parent device and GPU instance the C struct carries.
func (e *Engine) ComputeInstanceGetInfo(
	handle unsafe.Pointer,
) (nvml.ComputeInstanceInfo, unsafe.Pointer, unsafe.Pointer, nvml.Return) {
	ci, ret := e.computeInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return nvml.ComputeInstanceInfo{}, nil, nil, ret
	}
	info, ret := ci.GetInfo()
	if ret != nvml.SUCCESS {
		return nvml.ComputeInstanceInfo{}, nil, nil, ret
	}

	var giHandle unsafe.Pointer
	if info.GpuInstance != nil {
		giHandle, ret = registerHandle(e.gpuInstances, info.GpuInstance)
		if ret != nvml.SUCCESS {
			return nvml.ComputeInstanceInfo{}, nil, nil, ret
		}
	}
	return info, e.handleForDevice(info.Device), giHandle, nvml.SUCCESS
}

// ComputeInstanceDestroy tears down a compute instance, invalidating its
// handle and the MIG device it backed.
func (e *Engine) ComputeInstanceDestroy(handle unsafe.Pointer) nvml.Return {
	ci, ret := e.computeInstanceOf(handle)
	if ret != nvml.SUCCESS {
		return ret
	}

	// As in GpuInstanceDestroy, the MIG device this backed has to be named
	// before the teardown evicts it from the parent's cache.
	info, infoRet := ci.GetInfo()
	var migDevices []*ConfigurableDevice
	if infoRet == nvml.SUCCESS {
		giID := uint32(0)
		if info.GpuInstance != nil {
			if giInfo, ret := info.GpuInstance.GetInfo(); ret == nvml.SUCCESS {
				giID = giInfo.Id
			}
		}
		migDevices = migDevicesDerivedFrom(info.Device, giID, &info.Id)
	}

	if ret := ci.Destroy(); ret != nvml.SUCCESS {
		return ret
	}

	e.retireMigDeviceHandles(migDevices)
	e.computeInstances.Retire(ci)
	return nvml.SUCCESS
}

// =============================================================================
// MIG devices
// =============================================================================

// DeviceGetMigDeviceHandleByIndex returns a device handle for the MIG device
// at the given index on a partitioned GPU.
func (e *Engine) DeviceGetMigDeviceHandleByIndex(handle unsafe.Pointer, index int) (unsafe.Pointer, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	migDev, ret := dev.GetMigDeviceHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return registerHandle(e.handles, migDev)
}

// DeviceGetDeviceHandleFromMigDeviceHandle returns a handle for the physical
// GPU behind a MIG device.
func (e *Engine) DeviceGetDeviceHandleFromMigDeviceHandle(handle unsafe.Pointer) (unsafe.Pointer, nvml.Return) {
	dev := e.LookupConfigurableDevice(handle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	parent, ret := dev.GetDeviceHandleFromMigDeviceHandle()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	// The parent is normally already registered — the caller reached the MIG
	// device through it — so this returns the handle it already holds.
	return registerHandle(e.handles, parent)
}

// migDevicesDerivedFrom lists the MIG devices minted for a GPU instance, or
// for one compute instance within it when ciID is non-nil.
//
// It must be called before the instance is destroyed: tearing an instance down
// evicts its MIG devices from the parent's cache, so afterwards there is
// nothing left to name.
func migDevicesDerivedFrom(parent nvml.Device, giID uint32, ciID *uint32) []*ConfigurableDevice {
	dev, ok := parent.(*ConfigurableDevice)
	if !ok || dev.migState == nil {
		return nil
	}
	return dev.migState.devicesDerivedFrom(giID, ciID)
}

// retireMigDeviceHandles invalidates the device handles of MIG devices whose
// backing instance is gone.
//
// Without this a caller that destroys an instance and keeps using the MIG
// device handle it already resolved would keep getting answers, where real
// NVML fails the call. The handle is retired rather than freed, so the stale
// pointer can never alias a MIG device created later.
func (e *Engine) retireMigDeviceHandles(devices []*ConfigurableDevice) {
	for _, migDev := range devices {
		e.handles.Retire(migDev)
	}
}

// handleForDevice returns the handle a device is already registered under,
// registering it if the caller somehow reached it without one.
func (e *Engine) handleForDevice(dev nvml.Device) unsafe.Pointer {
	if dev == nil {
		return nil
	}
	if handle := e.handles.HandleFor(dev); handle != nil {
		return handle
	}
	return e.handles.Register(dev)
}

// allComputeInstances lists every compute instance of a GPU instance
// regardless of profile, which the per-profile NVML query cannot do. Used
// only to know which handles a GPU instance teardown must invalidate.
func allComputeInstances(gi nvml.GpuInstance) []nvml.ComputeInstance {
	mock, ok := gi.(*mockserver.GpuInstance)
	if !ok {
		return nil
	}

	live := liveComputeInstances(mock)
	instances := make([]nvml.ComputeInstance, 0, len(live))
	for _, ci := range live {
		instances = append(instances, ci)
	}
	return instances
}
