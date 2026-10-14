// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// VgpuType resolves a vGPU type ID against the visible GPUs. A type ID names
// the same type on every GPU that declares it, so the first match answers.
func (e *Engine) VgpuType(id uint32) (VgpuType, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 || e.server == nil {
		return VgpuType{}, nvml.ERROR_UNINITIALIZED
	}
	for i, d := range e.server.configurableDevices {
		if d == nil || !e.server.isDeviceVisible(i) {
			continue
		}
		if t, ret := d.VgpuType(id); ret == nvml.SUCCESS {
			return t, nvml.SUCCESS
		}
	}
	return VgpuType{}, nvml.ERROR_INVALID_ARGUMENT
}

// VgpuInstance resolves a vGPU instance ID. The ID encodes its GPU, so a GPU
// the caller cannot see answers as an unknown instance.
func (e *Engine) VgpuInstance(id uint32) (VgpuInstance, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 || e.server == nil {
		return VgpuInstance{}, nvml.ERROR_UNINITIALIZED
	}
	index := int(id/vgpuInstanceIDStride) - 1
	if index < 0 || index >= len(e.server.configurableDevices) || !e.server.isDeviceVisible(index) {
		return VgpuInstance{}, nvml.ERROR_INVALID_ARGUMENT
	}
	d := e.server.configurableDevices[index]
	if d == nil {
		return VgpuInstance{}, nvml.ERROR_INVALID_ARGUMENT
	}
	v, ret := d.vgpuHost()
	switch ret {
	case nvml.SUCCESS:
	case nvml.ERROR_NOT_SUPPORTED:
		return VgpuInstance{}, nvml.ERROR_INVALID_ARGUMENT
	default:
		return VgpuInstance{}, ret
	}
	inst, ok := d.vgpuInstance(v.VGPU, vgpuInstanceSlot(id))
	if !ok {
		return VgpuInstance{}, nvml.ERROR_INVALID_ARGUMENT
	}
	// Engine.SystemGetDriverVersion would take e.mu a second time.
	version, ret := e.server.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return VgpuInstance{}, ret
	}
	inst.GuestDriverVersion = version
	return inst, nvml.SUCCESS
}
