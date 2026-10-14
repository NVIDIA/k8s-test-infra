// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const slotTestConfig = `
version: "1.0"
system:
  driver_version: "580.65.06"
  num_devices: 1
device_defaults:
  memory:
    total_bytes: 42949672960
  virtualization:
%s
`

const slotHostVirtualization = `    mode: host_vgpu
    host_vgpu_mode: sriov
    vgpu:
      types:
        - {id: 474, name: "NVIDIA A100-4C", framebuffer_mib: 4096, max_instances: 10}
      instances:
        - {type: "NVIDIA A100-4C", count: 2}`

const slotGuestVirtualization = `    mode: vgpu
    vgpu:
      guest:
        host_driver_version: "550.163.01"
        host_driver_branch: "r550_00"
        license: {product: "NVIDIA Virtual Compute Server", state: licensed}`

const slotBareMetalVirtualization = `    mode: none
    host_vgpu_mode: "N/A"`

// readSlot loads virtualization into the process-global engine the export
// table reads, makes one call to slot against GPU 0 and returns what the slot
// wrote. The engine is global, so these tests cannot run in parallel.
func readSlot(t *testing.T, slot uint, virtualization string) uint32 {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(slotTestConfig, virtualization)), 0o600))
	t.Setenv("MOCK_NVML_CONFIG", path)
	t.Setenv("MOCK_NVML_OVERRIDES", filepath.Join(t.TempDir(), "overrides.yaml"))
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })

	h, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	got, ret := internalSlotReadForTest(slot, h)
	require.Equal(t, nvml.SUCCESS, ret)
	return got
}

// `nvidia-smi vgpu` asks slot 72 before any public vGPU call and prints "No
// supported devices in vGPU mode" unless it answers 1.
func TestVgpuHostSlot(t *testing.T) {
	for name, tc := range map[string]struct {
		virtualization string
		want           uint32
	}{
		"host":       {slotHostVirtualization, 1},
		"guest":      {slotGuestVirtualization, 0},
		"bare metal": {slotBareMetalVirtualization, 0},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, readSlot(t, vgpuHostSlotForTest, tc.virtualization))
		})
	}
}

// `nvidia-smi -q` renders a guest's License Status from slot 189; any other
// GPU falls through to the zero answer every unhandled device slot gets.
func TestGuestLicenseStateSlot(t *testing.T) {
	for name, tc := range map[string]struct {
		virtualization string
		want           uint32
	}{
		"licensed guest": {slotGuestVirtualization, nvml.GRID_LICENSE_STATE_LICENSED},
		"host":           {slotHostVirtualization, 0},
		"bare metal":     {slotBareMetalVirtualization, 0},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, readSlot(t, guestLicenseStateSlotForTest, tc.virtualization))
		})
	}
}
