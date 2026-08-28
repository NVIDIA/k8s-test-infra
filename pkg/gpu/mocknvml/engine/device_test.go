// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestConfigurableDevice_GetBAR1MemoryInfo(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	bar1, ret := enhanced.GetBAR1MemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetBAR1MemoryInfo failed")

	expectedBytes := uint64(DefaultBAR1SizeMB * 1024 * 1024)
	require.Equal(t, expectedBytes, bar1.Bar1Total, "Expected BAR1 total")
	require.Equal(t, expectedBytes, bar1.Bar1Free, "Expected BAR1 free")
	require.Zero(t, bar1.Bar1Used, "Expected BAR1 used 0")
}

func TestConfigurableDevice_GetComputeRunningProcesses(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	processes, ret := enhanced.GetComputeRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret, "GetComputeRunningProcesses failed")
	// Mock returns empty list
	require.Empty(t, processes, "Expected empty process list")
}

func TestConfigurableDevice_GetGraphicsRunningProcesses(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	processes, ret := enhanced.GetGraphicsRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret, "GetGraphicsRunningProcesses failed")
	// Mock returns empty list
	require.Empty(t, processes, "Expected empty process list")
}

func TestConfigurableDevice_GetPciInfo(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	pciInfo, ret := enhanced.GetPciInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetPciInfo failed")

	// Verify PCI device ID is set (A100)
	require.Equal(t, uint32(0x20B010DE), pciInfo.PciDeviceId, "Expected A100 PCI device ID")
}

// TestConfigurableDevice_GetPciInfo_BusIDDomainWidths verifies that GetPciInfo
// populates BOTH bus-ID strings, each in the format nvml.h defines for it:
// busId uses an 8-digit domain (NVML_DEVICE_PCI_BUS_ID_FMT, "%08X:%02X:%02X.0")
// and busIdLegacy a 4-digit one (NVML_DEVICE_PCI_BUS_ID_LEGACY_FMT).
//
// Both fields matter to real consumers. NVSentinel's metadata-collector derives
// each GPU's pci_address from busIdLegacy, so leaving it empty produces blank
// PCI addresses. And the domain width of busId is load-bearing rather than
// cosmetic: go-nvlib recovers a sysfs BDF by stripping a leading "0000" from
// busId, so a 4-digit domain there leaves the malformed ":3b:00.0" and every
// /sys/bus/pci lookup built from it fails — which is how GPU Operator's GFD
// ends up labelling nvidia.com/gpu.mode=unknown.
func TestConfigurableDevice_GetPciInfo_BusIDDomainWidths(t *testing.T) {
	// Profiles declare the canonical Linux sysfs BDF; only busId widens.
	const busID = "0000:3b:00.0"
	yaml := &YAMLConfig{
		System: SystemConfig{DriverVersion: "550.0", NumDevices: 1},
		Devices: []DeviceOverride{
			{Index: 0, DeviceConfig: DeviceConfig{PCI: &PCIConfig{BusID: busID}}},
		},
	}
	cfg := &Config{NumDevices: 1, DriverVersion: "550.0", YAMLConfig: yaml}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	pci, ret := cd.GetPciInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetPciInfo failed")
	require.Equal(t, "00000000:3B:00.0", busIDString(pci.BusId[:]),
		"busId must use the 8-digit NVML domain form")
	require.Equal(t, "0000:3B:00.0", busIDString(pci.BusIdLegacy[:]),
		"busIdLegacy must use the 4-digit domain form")
}

// =============================================================================
// Topology Tests (from T4/Batch 1)
// =============================================================================

func TestConfigurableDevice_GetTopologyCommonAncestor(t *testing.T) {
	cfg := &Config{
		NumDevices:    2,
		DriverVersion: "550.0",
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle1, _ := e.DeviceGetHandleByIndex(0)
	handle2, _ := e.DeviceGetHandleByIndex(1)
	dev1 := e.LookupDevice(handle1)
	dev2 := e.LookupDevice(handle2)

	cd1, ok := dev1.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	// Default: devices on same node should return TOPOLOGY_SINGLE
	level, ret := cd1.GetTopologyCommonAncestor(dev2)
	require.Equal(t, nvml.SUCCESS, ret, "GetTopologyCommonAncestor failed")
	require.Equal(t, nvml.TOPOLOGY_SINGLE, level, "Expected TOPOLOGY_SINGLE")
}

func TestConfigurableDevice_GetTopologyCommonAncestor_WithConfig(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    2,
		},
		DeviceDefaults: DeviceConfig{
			Topology: &TopologyConfig{
				DefaultLevel: "system",
			},
		},
	}
	cfg := &Config{
		NumDevices:    2,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle1, _ := e.DeviceGetHandleByIndex(0)
	handle2, _ := e.DeviceGetHandleByIndex(1)
	dev1 := e.LookupDevice(handle1)
	dev2 := e.LookupDevice(handle2)

	cd1, ok := dev1.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	level, ret := cd1.GetTopologyCommonAncestor(dev2)
	require.Equal(t, nvml.SUCCESS, ret, "GetTopologyCommonAncestor failed")
	require.Equal(t, nvml.TOPOLOGY_SYSTEM, level, "Expected TOPOLOGY_SYSTEM")
}

// =============================================================================
// NVLink Tests (from T4/Batch 1)
// =============================================================================

func TestConfigurableDevice_GetNvLinkState_WithConfig(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    1,
		},
		NVLink: &NVLinkConfig{
			LinksPerGPU: 6,
			Links: []NVLinkLinkConfig{
				{Link: 0, State: "active"},
				{Link: 1, State: "inactive"},
			},
		},
	}
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	// Link 0 should be active
	state, ret := cd.GetNvLinkState(0)
	require.Equal(t, nvml.SUCCESS, ret, "GetNvLinkState(0) failed")
	require.Equal(t, nvml.FEATURE_ENABLED, state, "Expected link 0 ENABLED")

	// Link 1 should be inactive
	state, ret = cd.GetNvLinkState(1)
	require.Equal(t, nvml.SUCCESS, ret, "GetNvLinkState(1) failed")
	require.Equal(t, nvml.FEATURE_DISABLED, state, "Expected link 1 DISABLED")
}

func TestConfigurableDevice_GetNvLinkErrorCounter(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	// Error counter should always return 0
	val, ret := cd.GetNvLinkErrorCounter(0, 0)
	require.Equal(t, nvml.SUCCESS, ret, "GetNvLinkErrorCounter failed")
	require.Zero(t, val, "Expected 0")
}

func TestConfigurableDevice_GetNvLinkRemotePciInfo(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    1,
		},
		NVLink: &NVLinkConfig{
			LinksPerGPU: 6,
			Links: []NVLinkLinkConfig{
				{Link: 0, State: "active", RemotePCIBusID: "0000:3B:00.0"},
			},
		},
	}
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	pci, ret := cd.GetNvLinkRemotePciInfo(0)
	require.Equal(t, nvml.SUCCESS, ret, "GetNvLinkRemotePciInfo failed")
	require.Equal(t, uint32(0x3B), pci.Bus, "Expected bus 0x3B")
	// Each field carries the remote BDF in its own NVML format, as the local
	// address does. busIdLegacy must be populated too — the metadata-collector
	// reads it to build each NVLink's remote_pci_address.
	require.Equal(t, "00000000:3B:00.0", busIDString(pci.BusId[:]), "remote BusId not populated")
	require.Equal(t, "0000:3B:00.0", busIDString(pci.BusIdLegacy[:]), "remote BusIdLegacy not populated")
}

// =============================================================================
// Thermal Tests (from T4/Batch 1)
// =============================================================================

func TestConfigurableDevice_GetTemperatureThreshold_WithConfig(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    1,
		},
		DeviceDefaults: DeviceConfig{
			Thermal: &ThermalConfig{
				ShutdownThreshold_C: 95,
				SlowdownThreshold_C: 90,
				MaxOperating_C:      83,
			},
		},
	}
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	temp, ret := cd.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SHUTDOWN)
	require.Equal(t, nvml.SUCCESS, ret, "GetTemperatureThreshold(SHUTDOWN) failed")
	require.Equal(t, uint32(95), temp, "Expected 95")

	temp, ret = cd.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SLOWDOWN)
	require.Equal(t, nvml.SUCCESS, ret, "GetTemperatureThreshold(SLOWDOWN) failed")
	require.Equal(t, uint32(90), temp, "Expected 90")
}

func TestConfigurableDevice_GetThermalSettings(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    1,
		},
		DeviceDefaults: DeviceConfig{
			Thermal: &ThermalConfig{
				TemperatureGPU_C: 45,
				MaxOperating_C:   83,
			},
		},
	}
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	settings, ret := cd.GetThermalSettings(0)
	require.Equal(t, nvml.SUCCESS, ret, "GetThermalSettings failed")
	require.Equal(t, uint32(1), settings.Count, "Expected 1 sensor")
}

// =============================================================================
// Power Tests (from T4/Batch 1)
// =============================================================================

func TestConfigurableDevice_GetEnforcedPowerLimit_WithConfig(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    1,
		},
		DeviceDefaults: DeviceConfig{
			Power: &PowerConfig{
				EnforcedLimitMW: 300000,
			},
		},
	}
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	limit, ret := cd.GetEnforcedPowerLimit()
	require.Equal(t, nvml.SUCCESS, ret, "GetEnforcedPowerLimit failed")
	require.Equal(t, uint32(300000), limit, "Expected 300000")
}

func TestConfigurableDevice_GetPowerManagementMode(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{
			DriverVersion: "550.0",
			NumDevices:    1,
		},
		DeviceDefaults: DeviceConfig{
			Power: &PowerConfig{
				ManagementMode: "enabled",
			},
		},
	}
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.0",
		YAMLConfig:    yaml,
	}
	e := NewEngine(cfg)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	mode, ret := cd.GetPowerManagementMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPowerManagementMode failed")
	require.Equal(t, nvml.FEATURE_ENABLED, mode, "Expected FEATURE_ENABLED")
}

func TestConfigurableDevice_GetPowerManagementMode_Default(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")

	mode, ret := cd.GetPowerManagementMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPowerManagementMode failed")
	require.Equal(t, nvml.FEATURE_DISABLED, mode, "Expected FEATURE_DISABLED")
}

// =============================================================================
// Batch 2 Test Helper
// =============================================================================

// newTestDeviceWithConfig creates a test engine with YAML config and returns the first device.
func newTestDeviceWithConfig(t *testing.T, deviceCfg *DeviceConfig) *ConfigurableDevice {
	t.Helper()
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "550.163",
				NVMLVersion:   "12.550.163",
				NumDevices:    1,
			},
			DeviceDefaults: *deviceCfg,
		},
	}
	e := NewEngine(cfg)
	_ = e.Init()
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)
	cd, ok := dev.(*ConfigurableDevice)
	require.True(t, ok, "Expected ConfigurableDevice type")
	return cd
}

// =============================================================================
// Process functions (Batch 2)
// =============================================================================

func TestConfigurableDevice_GetComputeRunningProcesses_WithConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Processes: []ProcessConfig{
			{PID: 1234, Type: "C", Name: "python", UsedMemoryMiB: 1024},
			{PID: 5678, Type: "C", Name: "torch", UsedMemoryMiB: 2048},
			{PID: 9999, Type: "G", Name: "Xorg", UsedMemoryMiB: 128},
		},
	})

	procs, ret := dev.GetComputeRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret, "GetComputeRunningProcesses failed")
	require.Len(t, procs, 2, "Expected 2 compute processes")
	require.Equal(t, uint32(1234), procs[0].Pid, "Expected PID 1234")
	require.Equal(t, uint64(1024*1024*1024), procs[0].UsedGpuMemory, "Expected 1 GiB memory")
	require.Equal(t, uint32(5678), procs[1].Pid, "Expected PID 5678")
}

func TestConfigurableDevice_ProcessByPID(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Processes: []ProcessConfig{
			{PID: 1234, Type: "C", Name: "python", UsedMemoryMiB: 1024, SmUtil: 42},
			{PID: 9999, Type: "G", Name: "Xorg", UsedMemoryMiB: 128},
			{PID: 5555, Type: "C", UsedMemoryMiB: 64},
		},
	})

	// Both process types resolve: the internal process list nvidia-smi reads is
	// not filtered by type.
	p, ok := dev.ProcessByPID(1234)
	require.True(t, ok)
	require.Equal(t, "python", p.Name)
	require.Equal(t, uint64(1024), p.UsedMemoryMiB)
	require.Equal(t, uint32(42), p.SmUtil, "every configured field must reach the caller")

	p, ok = dev.ProcessByPID(9999)
	require.True(t, ok)
	require.Equal(t, "Xorg", p.Name)

	// A configured process without a name is still found, so callers can tell it
	// apart from a pid this device does not run.
	p, ok = dev.ProcessByPID(5555)
	require.True(t, ok)
	require.Empty(t, p.Name)

	_, ok = dev.ProcessByPID(4321)
	require.False(t, ok, "unconfigured pid must not resolve")
}

func TestConfigurableDevice_GetProcessUtilization_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	utils, ret := dev.GetProcessUtilization(0)
	require.Equal(t, nvml.SUCCESS, ret, "GetProcessUtilization failed")
	require.Empty(t, utils, "Expected empty utilization list")
}

func TestConfigurableDevice_GetProcessUtilization_WithConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Processes: []ProcessConfig{
			{PID: 1234, Type: "C", Name: "python", UsedMemoryMiB: 1024, SmUtil: 75, MemUtil: 40},
			{PID: 9999, Type: "G", Name: "ffmpeg", UsedMemoryMiB: 128, EncUtil: 60, DecUtil: 30},
		},
	})

	utils, ret := dev.GetProcessUtilization(0)
	require.Equal(t, nvml.SUCCESS, ret, "GetProcessUtilization failed")
	// Both compute and graphics/video processes report utilization, like real NVML
	// (unlike GetComputeRunningProcesses, which is compute-only).
	require.Len(t, utils, 2, "Expected 2 utilization samples")
	require.Equal(t, uint32(1234), utils[0].Pid, "Expected PID 1234")
	require.Equal(t, uint32(75), utils[0].SmUtil, "Expected SmUtil 75")
	require.Equal(t, uint32(40), utils[0].MemUtil, "Expected MemUtil 40")
	require.NotZero(t, utils[0].TimeStamp, "Expected a non-zero timestamp")
	// The graphics/video process is reported too, carrying encoder/decoder util.
	require.Equal(t, uint32(9999), utils[1].Pid, "Expected PID 9999")
	require.Equal(t, uint32(60), utils[1].EncUtil, "Expected EncUtil 60")
	require.Equal(t, uint32(30), utils[1].DecUtil, "Expected DecUtil 30")
}

// =============================================================================
// Performance functions (Batch 2)
// =============================================================================

func TestConfigurableDevice_GetPerformanceState_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	pstate, ret := dev.GetPerformanceState()
	require.Equal(t, nvml.SUCCESS, ret, "GetPerformanceState failed")
	require.Equal(t, nvml.PSTATE_0, pstate, "Expected P0 default")
}

func TestConfigurableDevice_GetPerformanceState_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:             "NVIDIA A100-SXM4-80GB",
		PerformanceState: "P8",
	})

	pstate, ret := dev.GetPerformanceState()
	require.Equal(t, nvml.SUCCESS, ret, "GetPerformanceState failed")
	require.Equal(t, nvml.PSTATE_8, pstate, "Expected P8")
}

func TestConfigurableDevice_GetCurrentClocksEventReasons_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	reasons, ret := dev.GetCurrentClocksEventReasons()
	require.Equal(t, nvml.SUCCESS, ret, "GetCurrentClocksEventReasons failed")
	require.Zero(t, reasons, "Expected 0 (no throttling)")
}

// =============================================================================
// Persistence functions (Batch 2)
// =============================================================================

func TestConfigurableDevice_GetPersistenceMode_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	mode, ret := dev.GetPersistenceMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPersistenceMode failed")
	require.Equal(t, nvml.FEATURE_DISABLED, mode, "Expected DISABLED default")
}

func TestConfigurableDevice_GetPersistenceMode_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:            "NVIDIA A100-SXM4-80GB",
		PersistenceMode: "enabled",
	})

	mode, ret := dev.GetPersistenceMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPersistenceMode failed")
	require.Equal(t, nvml.FEATURE_ENABLED, mode, "Expected ENABLED")
}

func TestConfigurableDevice_SetPersistenceMode(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	// Initially disabled
	mode, ret := dev.GetPersistenceMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPersistenceMode failed")
	require.Equal(t, nvml.FEATURE_DISABLED, mode, "Expected DISABLED initially")

	// Set to enabled
	ret = dev.SetPersistenceMode(nvml.FEATURE_ENABLED)
	require.Equal(t, nvml.SUCCESS, ret, "SetPersistenceMode failed")

	// Now should be enabled
	mode, ret = dev.GetPersistenceMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPersistenceMode failed")
	require.Equal(t, nvml.FEATURE_ENABLED, mode, "Expected ENABLED after set")

	// Set back to disabled
	ret = dev.SetPersistenceMode(nvml.FEATURE_DISABLED)
	require.Equal(t, nvml.SUCCESS, ret, "SetPersistenceMode failed")

	mode, ret = dev.GetPersistenceMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetPersistenceMode failed")
	require.Equal(t, nvml.FEATURE_DISABLED, mode, "Expected DISABLED after unset")
}

// =============================================================================
// Advanced functions (Batch 2)
// =============================================================================

func TestConfigurableDevice_GetRemappedRows_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	corrRows, uncRows, isPending, failureOccurred, ret := dev.GetRemappedRows()
	require.Equal(t, nvml.SUCCESS, ret, "GetRemappedRows failed")
	require.Zero(t, corrRows, "Expected 0 correctable rows")
	require.Zero(t, uncRows, "Expected 0 uncorrectable rows")
	require.False(t, isPending, "Expected no pending")
	require.False(t, failureOccurred, "Expected no failure")
}

func TestConfigurableDevice_GetRemappedRows_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		RemappedRows: &RemappedRowsConfig{
			Correctable:     2,
			Uncorrectable:   1,
			Pending:         true,
			FailureOccurred: false,
		},
	})

	corrRows, uncRows, isPending, failureOccurred, ret := dev.GetRemappedRows()
	require.Equal(t, nvml.SUCCESS, ret, "GetRemappedRows failed")
	require.Equal(t, 2, corrRows, "Expected 2 correctable rows")
	require.Equal(t, 1, uncRows, "Expected 1 uncorrectable row")
	require.True(t, isPending, "Expected pending=true")
	require.False(t, failureOccurred, "Expected failure=false")
}

func TestConfigurableDevice_GetGspFirmwareMode_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	isEnabled, defaultMode, ret := dev.GetGspFirmwareMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetGspFirmwareMode failed")
	// Default: disabled
	require.False(t, isEnabled, "Expected GSP disabled by default")
	require.False(t, defaultMode, "Expected GSP default mode disabled by default")
}

func TestConfigurableDevice_GetGspFirmwareMode_Enabled(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		GSPFirmware: &GSPFirmwareConfig{
			Mode: "enabled",
		},
	})

	isEnabled, _, ret := dev.GetGspFirmwareMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetGspFirmwareMode failed")
	require.True(t, isEnabled, "Expected GSP enabled")
}

func TestConfigurableDevice_GetDisplayActive_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	active, ret := dev.GetDisplayActive()
	require.Equal(t, nvml.SUCCESS, ret, "GetDisplayActive failed")
	require.Equal(t, nvml.FEATURE_DISABLED, active, "Expected DISABLED default")
}

func TestConfigurableDevice_GetDisplayActive_Enabled(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Display: &DisplayConfig{
			Active: "enabled",
		},
	})

	active, ret := dev.GetDisplayActive()
	require.Equal(t, nvml.SUCCESS, ret, "GetDisplayActive failed")
	require.Equal(t, nvml.FEATURE_ENABLED, active, "Expected ENABLED")
}

// =============================================================================
// MIG Tests (Batch 3)
// =============================================================================

func TestConfigurableDevice_GetMaxMigDeviceCount_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	count, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret, "GetMaxMigDeviceCount failed")
	// Default: MIG disabled, count = 0
	require.Zero(t, count, "Expected 0 (MIG disabled)")
}

func TestConfigurableDevice_GetMaxMigDeviceCount_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		MIG: &MIGConfig{
			ModeCurrent:     "enabled",
			MaxGPUInstances: 7,
		},
	})

	count, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret, "GetMaxMigDeviceCount failed")
	require.Equal(t, 7, count, "Expected 7")
}

func TestConfigurableDevice_GetMigMode_WithConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		MIG: &MIGConfig{
			ModeCurrent: "enabled",
			ModePending: "disabled",
		},
	})

	current, pending, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetMigMode failed")
	require.Equal(t, 1, current, "Expected current=1 (enabled)")
	require.Zero(t, pending, "Expected pending=0 (disabled)")
}

func TestConfigurableDevice_GetMigDeviceHandleByIndex_MIGDisabled(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	// NOT_FOUND (not NOT_SUPPORTED) signals "no device at this index" which
	// callers like nvidia-device-plugin treat as end-of-iteration, not error.
	_, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "Expected NOT_FOUND when MIG disabled")
}

// =============================================================================
// GPM Tests (Batch 3)
// =============================================================================

// =============================================================================
// nvidia-smi -q Gap Closure Tests
// =============================================================================

func TestConfigurableDevice_GetMemoryBusWidth_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Memory: &MemoryConfig{
			TotalBytes:     42949672960,
			MemoryBusWidth: 5120,
		},
	})

	width, ret := dev.GetMemoryBusWidth()
	require.Equal(t, nvml.SUCCESS, ret, "GetMemoryBusWidth failed")
	require.Equal(t, uint32(5120), width, "Expected 5120")
}

func TestConfigurableDevice_GetMemoryBusWidth_NotConfigured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	_, ret := dev.GetMemoryBusWidth()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "Expected NOT_SUPPORTED when no memory config")
}

func TestConfigurableDevice_GetDefaultEccMode_Enabled(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		ECC: &ECCConfig{
			DefaultMode: "enabled",
		},
	})

	mode, ret := dev.GetDefaultEccMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetDefaultEccMode failed")
	require.Equal(t, nvml.FEATURE_ENABLED, mode, "Expected ENABLED")
}

func TestConfigurableDevice_GetDefaultEccMode_NoConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	mode, ret := dev.GetDefaultEccMode()
	require.Equal(t, nvml.SUCCESS, ret, "GetDefaultEccMode failed")
	require.Equal(t, nvml.FEATURE_DISABLED, mode, "Expected DISABLED when no ECC config")
}

func TestConfigurableDevice_GetSupportedClocksThrottleReasons(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	reasons, ret := dev.GetSupportedClocksThrottleReasons()
	require.Equal(t, nvml.SUCCESS, ret, "GetSupportedClocksThrottleReasons failed")
	require.Equal(t, uint64(nvml.ClocksThrottleReasonAll), reasons, "Expected ClocksThrottleReasonAll")
}

func TestConfigurableDevice_GetAutoBoostedClocksEnabled(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	_, _, ret := dev.GetAutoBoostedClocksEnabled()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "Expected NOT_SUPPORTED for datacenter GPU")
}

func TestConfigurableDevice_GetGspFirmwareVersion_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		GSPFirmware: &GSPFirmwareConfig{
			Version: "550.54.15",
		},
	})

	version, ret := dev.GetGspFirmwareVersion()
	require.Equal(t, nvml.SUCCESS, ret, "GetGspFirmwareVersion failed")
	require.Equal(t, "550.54.15", version, "Expected '550.54.15'")
}

func TestConfigurableDevice_GetGspFirmwareVersion_NoConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	_, ret := dev.GetGspFirmwareVersion()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "Expected NOT_SUPPORTED when no GSP config")
}

func TestConfigurableDevice_GetTotalEnergyConsumption_Configured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Power: &PowerConfig{
			TotalEnergyConsumptionMJ: 500000,
		},
	})

	energy, ret := dev.GetTotalEnergyConsumption()
	require.Equal(t, nvml.SUCCESS, ret, "GetTotalEnergyConsumption failed")
	require.Equal(t, uint64(500000), energy, "Expected 500000")
}

func TestConfigurableDevice_GetTotalEnergyConsumption_Zero(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Power: &PowerConfig{
			TotalEnergyConsumptionMJ: 0,
		},
	})

	energy, ret := dev.GetTotalEnergyConsumption()
	require.Equal(t, nvml.SUCCESS, ret, "GetTotalEnergyConsumption failed")
	require.Zero(t, energy, "Expected 0 (valid zero)")
}

func TestConfigurableDevice_GetTotalEnergyConsumption_NoPowerConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	_, ret := dev.GetTotalEnergyConsumption()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "Expected NOT_SUPPORTED when no power config")
}

func TestConfigurableDevice_GetDetailedEccErrors_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	counts, ret := dev.GetDetailedEccErrors(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC)
	require.Equal(t, nvml.SUCCESS, ret, "GetDetailedEccErrors failed")
	require.Zero(t, counts.L1Cache, "Expected L1Cache zero")
	require.Zero(t, counts.L2Cache, "Expected L2Cache zero")
	require.Zero(t, counts.DeviceMemory, "Expected DeviceMemory zero")
	require.Zero(t, counts.RegisterFile, "Expected RegisterFile zero")
}

func TestConfigurableDevice_GetGpmSupport_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	supported, ret := dev.GetGpmSupport()
	require.Equal(t, nvml.SUCCESS, ret, "GetGpmSupport failed")
	// Default: not supported
	require.Zero(t, supported, "Expected 0 (not supported)")
}

func TestParseArchitecture(t *testing.T) {
	tests := []struct {
		input    string
		expected nvml.DeviceArchitecture
	}{
		{"kepler", nvml.DEVICE_ARCH_KEPLER},
		{"maxwell", nvml.DEVICE_ARCH_MAXWELL},
		{"pascal", nvml.DEVICE_ARCH_PASCAL},
		{"volta", nvml.DEVICE_ARCH_VOLTA},
		{"turing", nvml.DEVICE_ARCH_TURING},
		{"ampere", nvml.DEVICE_ARCH_AMPERE},
		{"ada", nvml.DEVICE_ARCH_ADA},
		{"ada_lovelace", nvml.DEVICE_ARCH_ADA},
		{"hopper", nvml.DEVICE_ARCH_HOPPER},
		{"blackwell", nvml.DEVICE_ARCH_BLACKWELL},
		{"unknown_arch", nvml.DEVICE_ARCH_UNKNOWN},
		{"", nvml.DEVICE_ARCH_UNKNOWN},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseArchitecture(tt.input)
			require.Equal(t, tt.expected, got, "parseArchitecture(%q)", tt.input)
		})
	}
}

// =============================================================================
// Audit Fix Tests: Memory_v2 Version Encoding (C1)
// =============================================================================

func TestConfigurableDevice_GetMemoryInfo_v2_VersionEncoding(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Memory: &MemoryConfig{
			TotalBytes: 42949672960,
		},
	})

	mem, ret := dev.GetMemoryInfo_v2()
	require.Equal(t, nvml.SUCCESS, ret, "GetMemoryInfo_v2 failed")

	// NVML_STRUCT_VERSION(Memory, 2) = sizeof(nvmlMemory_v2_t) | (2 << 24)
	expectedVersion := uint32(unsafe.Sizeof(nvml.Memory_v2{})) | (2 << 24)
	require.Equal(t, expectedVersion, mem.Version, "Expected Version 0x%X (sizeof=%d | 2<<24)",
		expectedVersion, unsafe.Sizeof(nvml.Memory_v2{}))
}

// =============================================================================
// Audit Fix Tests: Zero-Value Sentinel Bug (T4)
// =============================================================================

func TestConfigurableDevice_GetTemperature_ZeroIsValid(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Thermal: &ThermalConfig{
			TemperatureGPU_C: 0,
		},
	})

	temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.SUCCESS, ret, "Expected SUCCESS when Thermal config exists with temp=0")
	require.Zero(t, temp, "Expected 0")
}

func TestConfigurableDevice_GetTemperature_NilConfigReturnsNotSupported(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "Expected NOT_SUPPORTED when no Thermal config")
}

func TestConfigurableDevice_GetPowerUsage_ZeroIsValid(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Power: &PowerConfig{
			CurrentDrawMW: 0,
		},
	})

	power, ret := dev.GetPowerUsage()
	require.Equal(t, nvml.SUCCESS, ret, "Expected SUCCESS when Power config exists with draw=0")
	require.Zero(t, power, "Expected 0")
}

func TestConfigurableDevice_GetClockInfo_ZeroIsValid(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
		Clocks: &ClocksConfig{
			GraphicsCurrent: 0,
		},
	})

	clock, ret := dev.GetClockInfo(nvml.CLOCK_GRAPHICS)
	require.Equal(t, nvml.SUCCESS, ret, "Expected SUCCESS when Clocks config exists with graphics=0")
	require.Zero(t, clock, "Expected 0")
}

// clockMatrixConfig carries a distinct value in every clock field, so a getter
// reading the wrong one is caught by the value rather than only by a nil check.
func clockMatrixConfig() *ClocksConfig {
	return &ClocksConfig{
		GraphicsCurrent:    345,
		GraphicsMax:        2070,
		GraphicsApp:        2060,
		GraphicsAppDefault: 2050,
		SMCurrent:          346,
		SMMax:              2071,
		MemoryCurrent:      3990,
		MemoryMax:          3996,
		MemoryApp:          3995,
		MemoryAppDefault:   3994,
		VideoCurrent:       1200,
		VideoMax:           1912,
	}
}

func TestConfigurableDevice_GetMaxCustomerBoostClock_GraphicsIsTheBoostMax(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:   "NVIDIA GB300",
		Clocks: clockMatrixConfig(),
	})

	clock, ret := dev.GetMaxCustomerBoostClock(nvml.CLOCK_GRAPHICS)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(2070), clock,
		"the OEM boost ceiling is graphics_max: every in-tree hardware capture reports "+
			"max_customer_boost_clocks equal to max_clocks")
}

// The real captures only ever carry a graphics body under
// <max_customer_boost_clocks>, so the other three domains stay unsupported
// rather than inventing a ceiling no board was observed to report.
func TestConfigurableDevice_GetMaxCustomerBoostClock_OnlyGraphicsIsReported(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:   "NVIDIA GB300",
		Clocks: clockMatrixConfig(),
	})

	for _, clockType := range []nvml.ClockType{nvml.CLOCK_SM, nvml.CLOCK_MEM, nvml.CLOCK_VIDEO} {
		_, ret := dev.GetMaxCustomerBoostClock(clockType)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "clock type %d", clockType)
	}
}

// A profile carrying no graphics_max must keep reporting N/A, which is what
// nvidia-smi renders for NOT_SUPPORTED. Deriving the ceiling must not conjure a
// 0 MHz reading for a profile that declared no maximum.
func TestConfigurableDevice_GetMaxCustomerBoostClock_UnconfiguredIsNotSupported(t *testing.T) {
	for name, clocks := range map[string]*ClocksConfig{
		"no clocks block": nil,
		"no graphics_max": {GraphicsCurrent: 345},
	} {
		t.Run(name, func(t *testing.T) {
			dev := newTestDeviceWithConfig(t, &DeviceConfig{Name: "Tesla T4", Clocks: clocks})

			_, ret := dev.GetMaxCustomerBoostClock(nvml.CLOCK_GRAPHICS)
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
		})
	}
}

// GetClock is a two-dimensional lookup over clock type x clock id. Every
// combination the clocks: block carries must answer, so a caller reaching for
// the generic getter sees the same values as the dedicated ones.
func TestConfigurableDevice_GetClock_AnswersTheWholeMatrix(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:   "NVIDIA GB300",
		Clocks: clockMatrixConfig(),
	})

	for _, tc := range []struct {
		name      string
		clockType nvml.ClockType
		clockID   nvml.ClockId
		want      uint32
	}{
		{"current graphics", nvml.CLOCK_GRAPHICS, nvml.CLOCK_ID_CURRENT, 345},
		{"current sm", nvml.CLOCK_SM, nvml.CLOCK_ID_CURRENT, 346},
		{"current mem", nvml.CLOCK_MEM, nvml.CLOCK_ID_CURRENT, 3990},
		{"current video", nvml.CLOCK_VIDEO, nvml.CLOCK_ID_CURRENT, 1200},
		{"app target graphics", nvml.CLOCK_GRAPHICS, nvml.CLOCK_ID_APP_CLOCK_TARGET, 2060},
		{"app target mem", nvml.CLOCK_MEM, nvml.CLOCK_ID_APP_CLOCK_TARGET, 3995},
		{"app default graphics", nvml.CLOCK_GRAPHICS, nvml.CLOCK_ID_APP_CLOCK_DEFAULT, 2050},
		{"app default mem", nvml.CLOCK_MEM, nvml.CLOCK_ID_APP_CLOCK_DEFAULT, 3994},
		{"customer boost max graphics", nvml.CLOCK_GRAPHICS, nvml.CLOCK_ID_CUSTOMER_BOOST_MAX, 2070},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock, ret := dev.GetClock(tc.clockType, tc.clockID)
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tc.want, clock)
		})
	}
}

// The combinations no clocks: key carries. Application and customer-boost
// clocks exist for a subset of the domains, and NOT_SUPPORTED is what real NVML
// answers for the rest.
func TestConfigurableDevice_GetClock_UncarriedCombinationsAreNotSupported(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:   "NVIDIA GB300",
		Clocks: clockMatrixConfig(),
	})

	for _, tc := range []struct {
		name      string
		clockType nvml.ClockType
		clockID   nvml.ClockId
	}{
		{"app target sm", nvml.CLOCK_SM, nvml.CLOCK_ID_APP_CLOCK_TARGET},
		{"app target video", nvml.CLOCK_VIDEO, nvml.CLOCK_ID_APP_CLOCK_TARGET},
		{"app default sm", nvml.CLOCK_SM, nvml.CLOCK_ID_APP_CLOCK_DEFAULT},
		{"app default video", nvml.CLOCK_VIDEO, nvml.CLOCK_ID_APP_CLOCK_DEFAULT},
		{"customer boost max sm", nvml.CLOCK_SM, nvml.CLOCK_ID_CUSTOMER_BOOST_MAX},
		{"customer boost max mem", nvml.CLOCK_MEM, nvml.CLOCK_ID_CUSTOMER_BOOST_MAX},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ret := dev.GetClock(tc.clockType, tc.clockID)
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
		})
	}
}

// nvml.h documents INVALID_ARGUMENT for an invalid clockType, which is what the
// _COUNT enum sentinels are. They name the size of the enum, not a clock.
func TestConfigurableDevice_GetClock_RejectsEnumSentinels(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name:   "NVIDIA GB300",
		Clocks: clockMatrixConfig(),
	})

	_, ret := dev.GetClock(nvml.CLOCK_COUNT, nvml.CLOCK_ID_CURRENT)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "CLOCK_COUNT is not a clock domain")

	_, ret = dev.GetClock(nvml.CLOCK_GRAPHICS, nvml.CLOCK_ID_COUNT)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "CLOCK_ID_COUNT is not a clock id")
}

// =============================================================================
// NVLink / topology / affinity getters wired to NodeFabric
// =============================================================================

// newFabricEngine builds a 2-GPU engine where device 0 has 2 switch links and
// 1 direct GPU link to device 1, plus a single-NUMA pcie_topology block.
func newFabricEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := &Config{
		NumDevices:    2,
		DriverVersion: "560.0",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System:  SystemConfig{DriverVersion: "560.0", NVMLVersion: "12.560.0", NumDevices: 2},
			DeviceDefaults: DeviceConfig{
				Name: "NVIDIA A100-SXM4-80GB",
			},
			Devices: []DeviceOverride{
				devWithBDF(0, "0000:0A:00.0"),
				devWithBDF(1, "0000:0B:00.0"),
			},
			NVLink: &NVLinkConfig{
				Version:              5,
				BandwidthPerLinkMbps: 100000,
				Switches:             []NVSwitchConfig{{BDF: "0000:0F:00.0"}},
				Defaults:             &NVLinkDefaults{State: "active", DutyCycle: 0.05},
				DeviceLinks: []DeviceLinksConfig{
					{Index: 0, Links: []NVLinkLinkConfig{
						{Link: 0, State: "active", RemoteDeviceType: "switch", RemotePCIBusID: "0000:0F:00.0"},
						{Link: 1, State: "active", RemoteDeviceType: "switch", RemotePCIBusID: "0000:0F:00.0"},
						{Link: 2, State: "active", RemoteDeviceType: "gpu", RemotePCIBusID: "0000:0B:00.0"},
					}},
				},
			},
			PCIeTopology: &PCIeTopologyConfig{
				CoresPerNUMA: 8,
				RootComplexes: []RootComplexConfig{
					{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:0A:00.0", "0000:0B:00.0"}},
				},
			},
		},
	}
	e := NewEngine(cfg)
	require.Equal(t, nvml.SUCCESS, e.Init(), "engine init")
	t.Cleanup(func() { _ = e.Shutdown() })
	return e
}

func fabricDevice(t *testing.T, e *Engine, index int) *ConfigurableDevice {
	t.Helper()
	handle, _ := e.DeviceGetHandleByIndex(index)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "device %d is not a ConfigurableDevice", index)
	return cd
}

func TestConfigurableDevice_NvLinkGetters_PerDevice(t *testing.T) {
	e := newFabricEngine(t)
	d0 := fabricDevice(t, e, 0)
	d1 := fabricDevice(t, e, 1)

	st, ret := d0.GetNvLinkState(0)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkState(0) ret")
	require.Equal(t, nvml.FEATURE_ENABLED, st, "d0.GetNvLinkState(0) state")
	// Link in range but not configured on device 0.
	st, ret = d0.GetNvLinkState(7)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkState(7) ret")
	require.Equal(t, nvml.FEATURE_DISABLED, st, "d0.GetNvLinkState(7) state")
	// Device 1 has no links of its own.
	st, ret = d1.GetNvLinkState(0)
	require.Equal(t, nvml.SUCCESS, ret, "d1.GetNvLinkState(0) ret")
	require.Equal(t, nvml.FEATURE_DISABLED, st, "d1.GetNvLinkState(0) state")
	// Out-of-range link index.
	_, ret = d0.GetNvLinkState(-1)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "d0.GetNvLinkState(-1)")
	_, ret = d0.GetNvLinkState(99)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "d0.GetNvLinkState(99)")

	v, ret := d0.GetNvLinkVersion(0)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkVersion(0) ret")
	require.Equal(t, uint32(5), v, "d0.GetNvLinkVersion(0) version")

	capability, ret := d0.GetNvLinkCapability(0, nvml.NVLINK_CAP_P2P_SUPPORTED)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkCapability(0,P2P) ret")
	require.Equal(t, uint32(1), capability, "d0.GetNvLinkCapability(0,P2P) capability")

	// Remote device type: link 0 is a switch, link 2 is a GPU.
	dt, ret := d0.GetNvLinkRemoteDeviceType(0)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkRemoteDeviceType(0) ret")
	require.Equal(t, nvml.NVLINK_DEVICE_TYPE_SWITCH, dt, "d0.GetNvLinkRemoteDeviceType(0) type")
	dt, ret = d0.GetNvLinkRemoteDeviceType(2)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkRemoteDeviceType(2) ret")
	require.Equal(t, nvml.NVLINK_DEVICE_TYPE_GPU, dt, "d0.GetNvLinkRemoteDeviceType(2) type")

	// Remote PCI info: link 2 points at device 1's BDF.
	pci, ret := d0.GetNvLinkRemotePciInfo(2)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNvLinkRemotePciInfo(2)")
	busID := busIDString(pci.BusId[:])
	require.True(t, containsPrefix(busID, "00000000:0B") || containsPrefix(busID, "00000000:0b"),
		"d0 link2 remote BusId: got %q, want 00000000:0B prefix", busID)
}

func TestConfigurableDevice_TopologyCommonAncestor_Pairwise(t *testing.T) {
	e := newFabricEngine(t)
	d0 := fabricDevice(t, e, 0)
	d1 := fabricDevice(t, e, 1)

	// Same root complex => TOPOLOGY_SINGLE.
	lvl, ret := d0.GetTopologyCommonAncestor(d1)
	require.Equal(t, nvml.SUCCESS, ret, "d0->d1 topo ret")
	require.Equal(t, nvml.TOPOLOGY_SINGLE, lvl, "d0->d1 topo level")
}

func TestConfigurableDevice_Affinity_FromFabric(t *testing.T) {
	e := newFabricEngine(t)
	d0 := fabricDevice(t, e, 0)

	node, ret := d0.GetNumaNodeId()
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetNumaNodeId ret")
	require.Zero(t, node, "d0.GetNumaNodeId node")

	mask, ret := d0.GetCpuAffinity(2)
	require.Equal(t, nvml.SUCCESS, ret, "d0.GetCpuAffinity")
	require.Len(t, mask, 2, "d0 cpu affinity length")
	require.Equal(t, uint(0xFF), mask[0], "d0 cpu affinity word0")

	mem, ret := d0.GetMemoryAffinity(1, nvml.AFFINITY_SCOPE_NODE)
	require.Equal(t, nvml.SUCCESS, ret, "d0 memory affinity ret")
	require.Equal(t, []uint{1}, mem, "d0 memory affinity mask")
}

func TestConfigurableDevice_NvLinkUtilizationCounter_Grows(t *testing.T) {
	e := newFabricEngine(t)
	d0 := fabricDevice(t, e, 0)

	rx, tx, ret := d0.GetNvLinkUtilizationCounter(0, 0)
	require.Equal(t, nvml.SUCCESS, ret, "GetNvLinkUtilizationCounter")
	require.Equal(t, tx, rx, "rx must equal tx")
	// Freeze/Reset are no-op successes.
	require.Equal(t, nvml.SUCCESS, d0.FreezeNvLinkUtilizationCounter(0, 0, nvml.FEATURE_ENABLED), "Freeze")
	require.Equal(t, nvml.SUCCESS, d0.ResetNvLinkUtilizationCounter(0, 0), "Reset")
}

// busIDString decodes the NVML PciInfo.BusId char array (go-nvml v0.13.1-0
// types it as [32]int8) into a Go string, stopping at the NUL terminator.
func busIDString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func containsPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
