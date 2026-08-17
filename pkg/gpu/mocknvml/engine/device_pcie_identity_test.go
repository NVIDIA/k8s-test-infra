// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// pcieIdentityEngine builds an engine from shared device defaults plus
// per-device overrides, mirroring how the shipped profiles are shaped: the
// pcie block lives in device_defaults, bus_id is per device.
func pcieIdentityEngine(t *testing.T, defaults DeviceConfig, devices ...DeviceOverride) *Engine {
	t.Helper()
	cfg := &Config{
		NumDevices:    len(devices),
		DriverVersion: "550.163.01",
		YAMLConfig: &YAMLConfig{
			System:         SystemConfig{DriverVersion: "550.163.01", NumDevices: len(devices)},
			DeviceDefaults: defaults,
			Devices:        devices,
		},
	}
	e := NewEngine(cfg)
	require.Equal(t, nvml.SUCCESS, e.Init(), "engine Init failed")
	t.Cleanup(func() { _ = e.Shutdown() })
	return e
}

func pcieIdentityDevice(t *testing.T, e *Engine, index int) *ConfigurableDevice {
	t.Helper()
	handle, ret := e.DeviceGetHandleByIndex(index)
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetHandleByIndex(%d) failed", index)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "device %d is not a *ConfigurableDevice", index)
	return cd
}

// TestConfigurableDevice_GetGpuMaxPcieLinkGeneration reports the profile's
// configured max PCIe generation. nvidia-smi renders this as the "Device Max"
// row; when the query fails it prints its zero-initialised buffer, so a missing
// implementation shows an impossible PCIe generation of 0. "Host Max" is a
// separate reading — see TestConfigurableDevice_HostMaxPcieLinkGeneration.
// Issue #638.
func TestConfigurableDevice_GetGpuMaxPcieLinkGeneration(t *testing.T) {
	e := pcieIdentityEngine(t,
		DeviceConfig{PCIe: &PCIeConfig{MaxLinkGen: 4, CurrentLinkGen: 4}},
		DeviceOverride{Index: 0})

	gen, ret := pcieIdentityDevice(t, e, 0).GetGpuMaxPcieLinkGeneration()
	require.Equal(t, nvml.SUCCESS, ret, "GetGpuMaxPcieLinkGeneration failed")
	require.Equal(t, 4, gen, "device max PCIe generation")
}

// TestConfigurableDevice_GetGpuMaxPcieLinkGeneration_Gen6 pins the value to
// config rather than a hardcoded constant.
func TestConfigurableDevice_GetGpuMaxPcieLinkGeneration_Gen6(t *testing.T) {
	e := pcieIdentityEngine(t,
		DeviceConfig{PCIe: &PCIeConfig{MaxLinkGen: 6, CurrentLinkGen: 6}},
		DeviceOverride{Index: 0})

	gen, ret := pcieIdentityDevice(t, e, 0).GetGpuMaxPcieLinkGeneration()
	require.Equal(t, nvml.SUCCESS, ret, "GetGpuMaxPcieLinkGeneration failed")
	require.Equal(t, 6, gen, "device max PCIe generation")
}

// TestConfigurableDevice_GetGpuMaxPcieLinkGeneration_Unconfigured degrades to
// NOT_SUPPORTED — which nvidia-smi renders as N/A — rather than reporting 0.
// This matches GetMaxPcieLinkGeneration's existing behaviour.
func TestConfigurableDevice_GetGpuMaxPcieLinkGeneration_Unconfigured(t *testing.T) {
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})

	gen, ret := pcieIdentityDevice(t, e, 0).GetGpuMaxPcieLinkGeneration()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "unconfigured max_link_gen should not be supported")
	require.Zero(t, gen, "no generation reported when unsupported")
}

// TestConfigurableDevice_HostMaxPcieLinkGeneration backs nvidia-smi's "Host Max"
// row, which reads the host side of the link through an internal export-table
// slot rather than any public NVML API. The mock models a host that keeps up
// with the GPU, so it reports the configured max_link_gen. Issue #638.
func TestConfigurableDevice_HostMaxPcieLinkGeneration(t *testing.T) {
	for _, tc := range []struct {
		name string
		pcie *PCIeConfig
		want int
	}{
		{name: "gen4", pcie: &PCIeConfig{MaxLinkGen: 4, CurrentLinkGen: 4}, want: 4},
		{name: "gen6", pcie: &PCIeConfig{MaxLinkGen: 6, CurrentLinkGen: 6}, want: 6},
		// Unknown rather than an impossible Gen0: the bridge leaves the reading
		// alone when the profile configures no PCIe block.
		{name: "unconfigured", pcie: nil, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := pcieIdentityEngine(t, DeviceConfig{PCIe: tc.pcie}, DeviceOverride{Index: 0})

			require.Equal(t, tc.want, pcieIdentityDevice(t, e, 0).HostMaxPcieLinkGeneration(),
				"host max PCIe generation")
		})
	}
}

// TestConfigurableDevice_HostMaxPcieLinkGeneration_MatchesDeviceMax pins the
// invariant nvidia-smi's three generation rows have to satisfy: the negotiable
// "Max" can never exceed either endpoint's capability.
func TestConfigurableDevice_HostMaxPcieLinkGeneration_MatchesDeviceMax(t *testing.T) {
	e := pcieIdentityEngine(t,
		DeviceConfig{PCIe: &PCIeConfig{MaxLinkGen: 5, CurrentLinkGen: 4}},
		DeviceOverride{Index: 0})
	dev := pcieIdentityDevice(t, e, 0)

	deviceMax, ret := dev.GetGpuMaxPcieLinkGeneration()
	require.Equal(t, nvml.SUCCESS, ret, "GetGpuMaxPcieLinkGeneration failed")
	linkMax, ret := dev.GetMaxPcieLinkGeneration()
	require.Equal(t, nvml.SUCCESS, ret, "GetMaxPcieLinkGeneration failed")

	hostMax := dev.HostMaxPcieLinkGeneration()
	require.Equal(t, deviceMax, hostMax, "host and device max should agree")
	require.LessOrEqual(t, linkMax, min(deviceMax, hostMax), "link max exceeds an endpoint capability")
}

// TestConfigurableDevice_GetBoardId derives the board ID from the configured
// PCI address the way real NVML does: (domain << 16) | (bus << 8) | (device <<
// 3). A GPU at 0000:07:00.0 therefore reports 0x700, matching what nvidia-smi
// prints on real hardware. See issue #638.
func TestConfigurableDevice_GetBoardId(t *testing.T) {
	for _, tc := range []struct {
		busID string
		want  uint32
	}{
		{busID: "0000:07:00.0", want: 0x0700},
		{busID: "0000:BD:00.0", want: 0xBD00},
		{busID: "0001:4A:00.0", want: 0x14A00},
		{busID: "0000:3B:01.0", want: 0x3B08},
	} {
		t.Run(tc.busID, func(t *testing.T) {
			e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{
				Index:        0,
				DeviceConfig: DeviceConfig{PCI: &PCIConfig{BusID: tc.busID}},
			})

			boardID, ret := pcieIdentityDevice(t, e, 0).GetBoardId()
			require.Equal(t, nvml.SUCCESS, ret, "GetBoardId failed")
			require.Equal(t, tc.want, boardID, "board ID for %s", tc.busID)
		})
	}
}

// TestConfigurableDevice_GetBoardId_UniquePerDevice guards the property that
// actually matters to consumers: an eight-GPU node must not present eight
// identical board IDs.
func TestConfigurableDevice_GetBoardId_UniquePerDevice(t *testing.T) {
	busIDs := []string{
		"0000:07:00.0", "0000:0F:00.0", "0000:47:00.0", "0000:4E:00.0",
		"0000:87:00.0", "0000:90:00.0", "0000:B7:00.0", "0000:BD:00.0",
	}
	devices := make([]DeviceOverride, 0, len(busIDs))
	for i, busID := range busIDs {
		devices = append(devices, DeviceOverride{
			Index:        i,
			DeviceConfig: DeviceConfig{PCI: &PCIConfig{BusID: busID}},
		})
	}
	e := pcieIdentityEngine(t, DeviceConfig{}, devices...)

	seen := make(map[uint32]string, len(busIDs))
	for i, busID := range busIDs {
		boardID, ret := pcieIdentityDevice(t, e, i).GetBoardId()
		require.Equal(t, nvml.SUCCESS, ret, "GetBoardId failed for device %d", i)
		require.NotZero(t, boardID, "device %d (%s) reports board ID 0x0", i, busID)
		prev, dup := seen[boardID]
		require.False(t, dup, "device %d (%s) duplicates board ID %#x of %s", i, busID, boardID, prev)
		seen[boardID] = busID
	}
}

// TestConfigurableDevice_GetBoardId_AutoAssignedBusID covers profiles that omit
// bus_id: the engine auto-assigns 0000:<index+1>:00.0, so board IDs stay
// non-zero and distinct without any PCI configuration.
func TestConfigurableDevice_GetBoardId_AutoAssignedBusID(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.163.01"})
	require.Equal(t, nvml.SUCCESS, e.Init(), "engine Init failed")
	t.Cleanup(func() { _ = e.Shutdown() })

	seen := make(map[uint32]bool, 4)
	for i := range 4 {
		boardID, ret := pcieIdentityDevice(t, e, i).GetBoardId()
		require.Equal(t, nvml.SUCCESS, ret, "GetBoardId failed for device %d", i)
		require.NotZero(t, boardID, "device %d reports board ID 0x0", i)
		require.False(t, seen[boardID], "device %d duplicates board ID %#x", i, boardID)
		seen[boardID] = true
	}
	require.Len(t, seen, 4, "expected 4 distinct board IDs, got %v", seen)
}
