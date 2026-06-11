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

func TestConfigurableDevice_GetProcessUtilization_Default(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-80GB",
	})

	utils, ret := dev.GetProcessUtilization(0)
	require.Equal(t, nvml.SUCCESS, ret, "GetProcessUtilization failed")
	require.Empty(t, utils, "Expected empty utilization list")
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
