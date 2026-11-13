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
	return newVgpuEngineWithFailure(t, numDevices, virt, nil)
}

func newVgpuEngineWithFailure(t *testing.T, numDevices int, virt *VirtualizationConfig, failure *FailureInjectionConfig) *Engine {
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
				Failure:        failure,
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
	require.Equal(t, "Not Available", inst.VMDriverVersion(), "no driver runs in a simulated VM")
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

	_, ret = e.VgpuInstance(0)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "0 is never an instance ID")
	for _, id := range []uint32{999, 2003, 3001, 1 << 31} {
		_, ret := e.VgpuInstance(id)
		require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "id %d matches no running instance", id)
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
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// A runtime override of virtualization.mode skips load-time validation, so a
// GPU can claim host_vgpu with no vGPU block. Every vGPU call on it answers
// NOT_SUPPORTED, and so must the "is this a vGPU host" question nvidia-smi
// asks first.
func TestVgpuHost_ModeWithoutVgpuBlockIsNotAHost(t *testing.T) {
	t.Parallel()
	d := vgpuDevice(t, newVgpuEngine(t, 1, &VirtualizationConfig{
		Mode: VirtualizationModeHostVGPU, HostVGPUMode: "sriov",
	}), 0)

	require.False(t, d.IsVgpuHost())
	_, ret := d.GetHostVgpuMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// The load refuses a host with MIG on, and nvidia-smi -mig 1 must not get
// around it: the host's time-sliced vGPUs and MIG devices would then share
// one GPU.
func TestVgpuHost_RefusesMIGMode(t *testing.T) {
	t.Parallel()
	cfg := a100MIGConfig()
	cfg.Virtualization = vgpuHostVirtualization(2)
	d := newTestDeviceWithConfig(t, cfg)

	_, ret := d.SetMigMode(nvml.DEVICE_MIG_ENABLE)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	current, pending, ret := d.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_DISABLE, current)
	require.Equal(t, nvml.DEVICE_MIG_DISABLE, pending)

	_, ret = d.SetMigMode(nvml.DEVICE_MIG_DISABLE)
	require.Equal(t, nvml.SUCCESS, ret, "MIG is already off; disabling it is a no-op")
}

// The lookup decodes the slot from the ID, so it must find every instance the
// GPU lists, including those declared by a later entry.
func TestVgpuHost_InstanceLookupMatchesActiveList(t *testing.T) {
	t.Parallel()
	virt := vgpuHostVirtualization(2)
	virt.VGPU.Instances = append(virt.VGPU.Instances, VGPUInstanceConfig{Type: "NVIDIA A100-4C"})
	e := newVgpuEngine(t, 2, virt)
	d := vgpuDevice(t, e, 1)

	ids, ret := d.VgpuActiveInstanceIDs()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{2001, 2002, 2003}, ids)
	uuids := map[string]uint32{}
	for _, id := range ids {
		got, ret := e.VgpuInstance(id)
		require.Equal(t, nvml.SUCCESS, ret, "id %d", id)
		require.Equal(t, id, got.ID)
		require.Equal(t, uint32(474), got.Type.ID, "id %d", id)
		require.Equal(t, "NVIDIA A100-4C", got.Type.Name, "id %d", id)
		require.NotContains(t, uuids, got.UUID, "id %d reuses the UUID of %d", id, uuids[got.UUID])
		uuids[got.UUID] = id
	}
	for _, id := range []uint32{2000, 2004} {
		_, ret := e.VgpuInstance(id)
		require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "id %d", id)
	}
}

// A vGPU type presents its own PCI identity to the guest; each half falls back
// to the board's when the type does not declare it.
func TestVgpuHost_TypeOverridesPCIIdentity(t *testing.T) {
	t.Parallel()
	virt := vgpuHostVirtualization(1)
	virt.VGPU.Types[0].SubsystemID = 0x14A910DE
	virt.VGPU.Types[1].DeviceID = 0x20B710DE
	e := newVgpuEngine(t, 1, virt)

	sub, ret := e.VgpuType(474)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(0x20B010DE), sub.DeviceID, "board's device ID")
	require.Equal(t, uint64(0x14A910DE), sub.SubsystemID)

	dev, ret := e.VgpuType(475)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(0x20B710DE), dev.DeviceID)
	require.Equal(t, uint64(0x134F10DE), dev.SubsystemID, "board's subsystem ID")
}

// `nvidia-smi vgpu` truncates the VM ID, so instances on one GPU must already
// differ in the leading digits it keeps.
func TestVgpuHost_IdentitiesDifferInTheirLeadingDigits(t *testing.T) {
	t.Parallel()
	e := newVgpuEngine(t, 1, vgpuHostVirtualization(2))
	a, ret := e.VgpuInstance(1001)
	require.Equal(t, nvml.SUCCESS, ret)
	b, ret := e.VgpuInstance(1002)
	require.Equal(t, nvml.SUCCESS, ret)

	require.NotEqual(t, a.UUID[:8], b.UUID[:8])
	require.NotEqual(t, a.VMID[:8], b.VMID[:8])
}

// A lost GPU surfaces through the vGPU getters, but they answer from static
// config and never count toward an after_calls trip: nvidia-smi calls them on
// every run, so ticking would trip the failure on a schedule set by which view
// the caller asked for.
func TestVgpu_LostGPU(t *testing.T) {
	t.Parallel()
	const afterCalls = 3
	failure := &FailureInjectionConfig{Mode: FailureModeLost, AfterCalls: afterCalls}

	t.Run("host", func(t *testing.T) {
		t.Parallel()
		e := newVgpuEngineWithFailure(t, 1, vgpuHostVirtualization(2), failure)
		d := vgpuDevice(t, e, 0)
		for range 2 * afterCalls {
			_, ret := d.GetHostVgpuMode()
			require.Equal(t, nvml.SUCCESS, ret)
			_, ret = d.VgpuSupportedTypeIDs()
			require.Equal(t, nvml.SUCCESS, ret)
			_, ret = d.VgpuCreatableTypeIDs()
			require.Equal(t, nvml.SUCCESS, ret)
			_, ret = d.VgpuActiveInstanceIDs()
			require.Equal(t, nvml.SUCCESS, ret)
			_, ret = d.VgpuType(474)
			require.Equal(t, nvml.SUCCESS, ret)
			_, ret = e.VgpuType(474)
			require.Equal(t, nvml.SUCCESS, ret)
			_, ret = e.VgpuInstance(1001)
			require.Equal(t, nvml.SUCCESS, ret)
		}
		tripLost(t, d, afterCalls)

		_, ret := d.GetHostVgpuMode()
		require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
		_, ret = e.VgpuInstance(1001)
		require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	})

	t.Run("guest", func(t *testing.T) {
		t.Parallel()
		d := vgpuDevice(t, newVgpuEngineWithFailure(t, 1,
			vgpuGuestVirtualization(&VGPULicenseConfig{Product: "p"}), failure), 0)
		for range 2 * afterCalls {
			_, ret := d.GetGridLicensableFeatures()
			require.Equal(t, nvml.SUCCESS, ret)
		}
		tripLost(t, d, afterCalls)

		_, ret := d.GetGridLicensableFeatures()
		require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	})
}

// tripLost proves the counter is untouched: only the afterCalls-th guarded
// call trips the GPU.
func tripLost(t *testing.T, d *ConfigurableDevice, afterCalls int) {
	t.Helper()
	for i := 1; i < afterCalls; i++ {
		_, ret := d.GetTemperature(nvml.TEMPERATURE_GPU)
		require.NotEqual(t, nvml.ERROR_GPU_IS_LOST, ret, "guarded call %d tripped early", i)
	}
	_, ret := d.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "guarded call %d", afterCalls)
}

func TestVgpuHost_UninitializedEngine(t *testing.T) {
	t.Parallel()
	e := NewEngine(nil)
	_, ret := e.VgpuType(474)
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
	_, ret = e.VgpuInstance(1001)
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
}
