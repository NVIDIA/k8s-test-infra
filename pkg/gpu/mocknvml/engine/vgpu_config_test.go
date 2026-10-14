// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const vgpuHostYAML = `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 1
device_defaults:
  memory:
    total_bytes: 42949672960
  virtualization:
    mode: host_vgpu
    host_vgpu_mode: sriov
    vgpu:
      types:
        - id: 474
          name: "NVIDIA A100-4C"
          framebuffer_mib: 4096
          max_instances: 10
        - id: 475
          name: "NVIDIA A100-5C"
          framebuffer_mib: 5120
          max_instances: 8
      instances:
        - type: "NVIDIA A100-4C"
          count: 2
`

const vgpuGuestYAML = `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 1
device_defaults:
  virtualization:
    mode: vgpu
    vgpu:
      guest:
        host_driver_version: "550.163.01"
        host_driver_branch: "r550_00"
        license:
          product: "NVIDIA Virtual Compute Server"
          state: licensed
          expiry: "2027-01-01T00:00:00Z"
`

func decodeVGPUConfig(t *testing.T, doc string, mutate func(v *VirtualizationConfig)) *YAMLConfig {
	t.Helper()
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(doc), &yc), "yaml decode")
	if mutate != nil {
		mutate(yc.DeviceDefaults.Virtualization)
	}
	return &yc
}

func TestValidateVGPU_AcceptsHostAndGuest(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateYAMLConfig(decodeVGPUConfig(t, vgpuHostYAML, nil)))
	require.NoError(t, validateYAMLConfig(decodeVGPUConfig(t, vgpuGuestYAML, nil)))
}

// Every shipped profile carries host_vgpu_mode "N/A" and no vgpu block; that
// must keep loading, and so must mode vgpu with no guest block, which predates
// the block.
func TestValidateVGPU_LeavesOtherModesAlone(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"none", "passthrough", "vgpu", "something-else"} {
		yc := decodeVGPUConfig(t, vgpuGuestYAML, func(v *VirtualizationConfig) {
			v.Mode = mode
			v.HostVGPUMode = "N/A"
			v.VGPU = nil
		})
		require.NoError(t, validateYAMLConfig(yc), "mode %q", mode)
	}
}

func TestValidateVGPU_RejectsInconsistentBlocks(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		doc    string
		mutate func(v *VirtualizationConfig)
		want   string
	}{
		"host without types": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types = nil },
			"device_defaults.virtualization.vgpu.types: mode host_vgpu needs at least one vGPU type"},
		"host without vgpu block": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU = nil },
			"device_defaults.virtualization.vgpu.types: mode host_vgpu needs at least one vGPU type"},
		"bad host_vgpu_mode": {vgpuHostYAML, func(v *VirtualizationConfig) { v.HostVGPUMode = "N/A" },
			`device_defaults.virtualization.host_vgpu_mode: "N/A" is not one of sriov, non_sriov`},
		"type without id": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[0].ID = 0 },
			"device_defaults.virtualization.vgpu.types[0].id is required"},
		"type without name": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[0].Name = "" },
			"device_defaults.virtualization.vgpu.types[0].name is required"},
		"duplicate id": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[1].ID = 474 },
			"device_defaults.virtualization.vgpu.types[1].id: 474 is declared twice"},
		"duplicate name": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[1].Name = "NVIDIA A100-4C" },
			`device_defaults.virtualization.vgpu.types[1].name: "NVIDIA A100-4C" is declared twice`},
		"no framebuffer": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[0].FramebufferMiB = 0 },
			"device_defaults.virtualization.vgpu.types[0].framebuffer_mib is required"},
		"max_instances out of range": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[0].MaxInstances = 1000 },
			"device_defaults.virtualization.vgpu.types[0].max_instances: 1000 is outside 1..999"},
		"unknown instance type": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Instances[0].Type = "NVIDIA A100-40C" },
			`device_defaults.virtualization.vgpu.instances[0].type: "NVIDIA A100-40C" is not a declared vGPU type`},
		"negative count": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Instances[0].Count = -1 },
			"device_defaults.virtualization.vgpu.instances[0].count: -1 is negative"},
		"count over max_instances": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Instances[0].Count = 11 },
			`device_defaults.virtualization.vgpu.instances: 11 instances of "NVIDIA A100-4C" exceed its max_instances 10`},
		"mixed types": {vgpuHostYAML, func(v *VirtualizationConfig) {
			v.VGPU.Instances = append(v.VGPU.Instances, VGPUInstanceConfig{Type: "NVIDIA A100-5C"})
		}, `device_defaults.virtualization.vgpu.instances[1].type: "NVIDIA A100-5C" differs from "NVIDIA A100-4C"; a GPU runs one vGPU type at a time`},
		"framebuffer over board memory": {vgpuHostYAML, func(v *VirtualizationConfig) { v.VGPU.Types[0].FramebufferMiB = 40960 },
			"device_defaults.virtualization.vgpu.instances: 81920 MiB of vGPU framebuffer exceeds the board's 40960 MiB"},
		"guest block on a host": {vgpuHostYAML, func(v *VirtualizationConfig) {
			v.VGPU.Guest = &VGPUGuestConfig{HostDriverVersion: "550", HostDriverBranch: "r550"}
		}, "device_defaults.virtualization.vgpu.guest: only valid under mode vgpu"},
		"types on a guest": {vgpuGuestYAML, func(v *VirtualizationConfig) {
			v.VGPU.Types = []VGPUTypeConfig{{ID: 1, Name: "x", FramebufferMiB: 1, MaxInstances: 1}}
		}, "device_defaults.virtualization.vgpu.types: only valid under mode host_vgpu"},
		"instances on a guest": {vgpuGuestYAML, func(v *VirtualizationConfig) {
			v.VGPU.Instances = []VGPUInstanceConfig{{Type: "x"}}
		}, "device_defaults.virtualization.vgpu.instances: only valid under mode host_vgpu"},
		"guest without version": {vgpuGuestYAML, func(v *VirtualizationConfig) { v.VGPU.Guest.HostDriverVersion = "" },
			"device_defaults.virtualization.vgpu.guest.host_driver_version is required"},
		"guest without branch": {vgpuGuestYAML, func(v *VirtualizationConfig) { v.VGPU.Guest.HostDriverBranch = "" },
			"device_defaults.virtualization.vgpu.guest.host_driver_branch is required"},
		"version too long": {vgpuGuestYAML, func(v *VirtualizationConfig) { v.VGPU.Guest.HostDriverVersion = "550.163.011" },
			`device_defaults.virtualization.vgpu.guest.host_driver_version: "550.163.011" is longer than 10 bytes`},
		"branch too long": {vgpuGuestYAML, func(v *VirtualizationConfig) { v.VGPU.Guest.HostDriverBranch = "r550_00_lts" },
			`device_defaults.virtualization.vgpu.guest.host_driver_branch: "r550_00_lts" is longer than 10 bytes`},
		"unknown license state": {vgpuGuestYAML, func(v *VirtualizationConfig) { v.VGPU.Guest.License.State = "expired" },
			`device_defaults.virtualization.vgpu.guest.license.state: "expired" is not one of licensed, unlicensed`},
		"bad expiry": {vgpuGuestYAML, func(v *VirtualizationConfig) { v.VGPU.Guest.License.Expiry = "2027-01-01" },
			`device_defaults.virtualization.vgpu.guest.license.expiry: "2027-01-01" is not an RFC 3339 time`},
		"product too long": {vgpuGuestYAML, func(v *VirtualizationConfig) {
			v.VGPU.Guest.License.Product = string(make([]byte, 128))
		}, "device_defaults.virtualization.vgpu.guest.license.product is longer than 127 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.EqualError(t, validateYAMLConfig(decodeVGPUConfig(t, tc.doc, tc.mutate)), tc.want)
		})
	}
}

func TestVirtualizationConfig_Guest(t *testing.T) {
	t.Parallel()
	guest := decodeVGPUConfig(t, vgpuGuestYAML, nil).DeviceDefaults.Virtualization
	require.Equal(t, "r550_00", guest.Guest().HostDriverBranch)

	host := decodeVGPUConfig(t, vgpuHostYAML, nil).DeviceDefaults.Virtualization
	require.Nil(t, host.Guest(), "a host has no guest view")

	var none *VirtualizationConfig
	require.Nil(t, none.Guest())
}
