// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func vgpuHostVirtualization(count int) *VirtualizationConfig {
	return &VirtualizationConfig{
		Mode:         VirtualizationModeHostVGPU,
		HostVGPUMode: "sriov",
		VGPU: &VGPUConfig{
			Types: []VGPUTypeConfig{
				{ID: 474, Name: "NVIDIA A100-4C", FramebufferMiB: 4096, MaxInstances: 10,
					License: "NVIDIA-Virtual-Compute-Server,9.0"},
				{ID: 475, Name: "NVIDIA A100-5C", FramebufferMiB: 5120, MaxInstances: 8, Class: "Quadro"},
			},
			Instances: []VGPUInstanceConfig{{Type: "NVIDIA A100-4C", Count: count}},
		},
	}
}

// newVgpuEngine starts an engine whose every GPU carries virt. The GPUs
// declare their UUIDs, as every shipped profile does: an undeclared one is
// random per load, and vGPU identities derive from it.
func newVgpuEngine(t *testing.T, numDevices int, virt *VirtualizationConfig) *Engine {
	t.Helper()
	devices := make([]DeviceOverride, numDevices)
	for i := range devices {
		devices[i] = DeviceOverride{Index: i, UUID: fmt.Sprintf("GPU-5647d1a0-0000-0000-0000-%012d", i)}
	}
	e := NewEngine(&Config{
		NumDevices:    numDevices,
		DriverVersion: "550.163.01",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System:  SystemConfig{DriverVersion: "550.163.01", NumDevices: numDevices},
			DeviceDefaults: DeviceConfig{
				Virtualization: virt,
				Memory:         &MemoryConfig{TotalBytes: 40 << 30},
				PCI:            &PCIConfig{DeviceID: 0x20B010DE, SubsystemID: 0x134F10DE},
			},
			Devices: devices,
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	return e
}

func vgpuDevice(t *testing.T, e *Engine, index int) *ConfigurableDevice {
	t.Helper()
	h, ret := e.DeviceGetHandleByIndex(index)
	require.Equal(t, nvml.SUCCESS, ret)
	d := e.LookupConfigurableDevice(h)
	require.NotNil(t, d)
	return d
}

func TestVgpuHost_DeviceSurface(t *testing.T) {
	t.Parallel()
	e := newVgpuEngine(t, 2, vgpuHostVirtualization(2))
	d := vgpuDevice(t, e, 1)

	require.True(t, d.IsVgpuHost())
	mode, ret := d.GetHostVgpuMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.HOST_VGPU_MODE_SRIOV, mode)

	supported, ret := d.VgpuSupportedTypeIDs()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{474, 475}, supported)

	active, ret := d.VgpuActiveInstanceIDs()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{2001, 2002}, active, "GPU 1's block starts at 2001")
}

func TestVgpuHost_CreatableFollowsTheActiveType(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		count int
		want  []uint32
	}{
		"partly filled: only the running type": {2, []uint32{474}},
		"full: nothing":                        {10, []uint32{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := vgpuDevice(t, newVgpuEngine(t, 1, vgpuHostVirtualization(tc.count)), 0)
			got, ret := d.VgpuCreatableTypeIDs()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("empty: every supported type", func(t *testing.T) {
		t.Parallel()
		virt := vgpuHostVirtualization(0)
		virt.VGPU.Instances = nil
		d := vgpuDevice(t, newVgpuEngine(t, 1, virt), 0)
		got, ret := d.VgpuCreatableTypeIDs()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, []uint32{474, 475}, got)
	})
}

func TestVgpuHost_TypeResolvesDefaults(t *testing.T) {
	t.Parallel()
	e := newVgpuEngine(t, 1, vgpuHostVirtualization(1))

	got, ret := e.VgpuType(474)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, VgpuType{
		ID: 474, Name: "NVIDIA A100-4C", Class: "Compute", License: "NVIDIA-Virtual-Compute-Server,9.0",
		FramebufferBytes: 4096 << 20, DeviceID: 0x20B010DE, SubsystemID: 0x134F10DE,
		MaxInstances: 10, MaxInstancesPerVM: 1, NumDisplayHeads: 1,
		ResolutionWidth: 4096, ResolutionHeight: 2160, GPUInstanceProfileID: 0xFFFFFFFF,
	}, got)

	quadro, ret := e.VgpuType(475)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "Quadro", quadro.Class, "a declared class is kept")

	_, ret = e.VgpuType(9999)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

func TestVgpuHost_InstanceLookup(t *testing.T) {
	t.Parallel()
	e := newVgpuEngine(t, 2, vgpuHostVirtualization(2))

	inst, ret := e.VgpuInstance(2002)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(2002), inst.ID)
	require.Equal(t, "NVIDIA A100-4C", inst.Type.Name)
	require.Equal(t, "550.163.01", inst.GuestDriverVersion)
	require.Equal(t, uint32(0xFFFFFFFF), inst.GPUInstanceID, "time-sliced, not MIG-backed")
	require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, inst.UUID)
	require.NotEqual(t, inst.UUID, inst.VMID)

	other, ret := e.VgpuInstance(2001)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotEqual(t, inst.UUID, other.UUID, "each slot has its own UUID")

	lic := inst.LicenseInfo()
	require.Equal(t, uint8(1), lic.IsLicensed)
	require.Equal(t, uint32(nvml.GRID_LICENSE_STATE_LICENSED), lic.CurrentState)
	require.Equal(t, uint8(nvml.GRID_LICENSE_EXPIRY_PERMANENT), lic.LicenseExpiry.Status)

	for _, id := range []uint32{0, 999, 2003, 3001, 1 << 31} {
		_, ret := e.VgpuInstance(id)
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "id %d", id)
	}
}

// Nothing is persisted, so the same config must give the same identities on
// every load — that is what keeps a VM UUID stable across nvidia-smi runs.
func TestVgpuHost_IdentityIsDeterministic(t *testing.T) {
	t.Parallel()
	a, ret := newVgpuEngine(t, 1, vgpuHostVirtualization(2)).VgpuInstance(1002)
	require.Equal(t, nvml.SUCCESS, ret)
	b, ret := newVgpuEngine(t, 1, vgpuHostVirtualization(2)).VgpuInstance(1002)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, a.UUID, b.UUID)
	require.Equal(t, a.VMID, b.VMID)
}

func TestVgpuHost_BareMetalGPUIsNotSupported(t *testing.T) {
	t.Parallel()
	e := newVgpuEngine(t, 1, &VirtualizationConfig{Mode: "none", HostVGPUMode: "N/A"})
	d := vgpuDevice(t, e, 0)

	require.False(t, d.IsVgpuHost())
	_, ret := d.GetHostVgpuMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = d.VgpuSupportedTypeIDs()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = d.VgpuCreatableTypeIDs()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = d.VgpuActiveInstanceIDs()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = e.VgpuType(474)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
	_, ret = e.VgpuInstance(1001)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

func TestVgpuHost_UninitializedEngine(t *testing.T) {
	t.Parallel()
	e := NewEngine(nil)
	_, ret := e.VgpuType(474)
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
	_, ret = e.VgpuInstance(1001)
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
}
