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

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

func TestConfigurableDevice_GetBAR1MemoryInfo(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	bar1, ret := enhanced.GetBAR1MemoryInfo()
	if ret != nvml.SUCCESS {
		t.Errorf("GetBAR1MemoryInfo failed: %v", ret)
	}

	expectedBytes := uint64(DefaultBAR1SizeMB * 1024 * 1024)
	if bar1.Bar1Total != expectedBytes {
		t.Errorf("Expected BAR1 total %d, got %d", expectedBytes, bar1.Bar1Total)
	}
	if bar1.Bar1Free != expectedBytes {
		t.Errorf("Expected BAR1 free %d, got %d", expectedBytes, bar1.Bar1Free)
	}
	if bar1.Bar1Used != 0 {
		t.Errorf("Expected BAR1 used 0, got %d", bar1.Bar1Used)
	}
}

func TestConfigurableDevice_GetComputeRunningProcesses(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	processes, ret := enhanced.GetComputeRunningProcesses()
	if ret != nvml.SUCCESS {
		t.Errorf("GetComputeRunningProcesses failed: %v", ret)
	}
	// Mock returns empty list
	if len(processes) != 0 {
		t.Errorf("Expected empty process list, got %d processes", len(processes))
	}
}

func TestConfigurableDevice_GetGraphicsRunningProcesses(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	processes, ret := enhanced.GetGraphicsRunningProcesses()
	if ret != nvml.SUCCESS {
		t.Errorf("GetGraphicsRunningProcesses failed: %v", ret)
	}
	// Mock returns empty list
	if len(processes) != 0 {
		t.Errorf("Expected empty process list, got %d processes", len(processes))
	}
}

func TestConfigurableDevice_GetPciInfo(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*ConfigurableDevice)
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	pciInfo, ret := enhanced.GetPciInfo()
	if ret != nvml.SUCCESS {
		t.Errorf("GetPciInfo failed: %v", ret)
	}

	// Verify PCI device ID is set (A100)
	if pciInfo.PciDeviceId != 0x20B010DE {
		t.Errorf("Expected A100 PCI device ID, got 0x%X", pciInfo.PciDeviceId)
	}
}

// =============================================================================
// Topology Tests
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	// Default: devices on same node should return TOPOLOGY_SINGLE
	level, ret := cd1.GetTopologyCommonAncestor(dev2)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetTopologyCommonAncestor failed: %v", ret)
	}
	if level != nvml.TOPOLOGY_SINGLE {
		t.Errorf("Expected TOPOLOGY_SINGLE (%d), got %d", nvml.TOPOLOGY_SINGLE, level)
	}
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	level, ret := cd1.GetTopologyCommonAncestor(dev2)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetTopologyCommonAncestor failed: %v", ret)
	}
	if level != nvml.TOPOLOGY_SYSTEM {
		t.Errorf("Expected TOPOLOGY_SYSTEM (%d), got %d", nvml.TOPOLOGY_SYSTEM, level)
	}
}

// =============================================================================
// NVLink Tests
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	// Link 0 should be active
	state, ret := cd.GetNvLinkState(0)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetNvLinkState(0) failed: %v", ret)
	}
	if state != nvml.FEATURE_ENABLED {
		t.Errorf("Expected link 0 ENABLED, got %d", state)
	}

	// Link 1 should be inactive
	state, ret = cd.GetNvLinkState(1)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetNvLinkState(1) failed: %v", ret)
	}
	if state != nvml.FEATURE_DISABLED {
		t.Errorf("Expected link 1 DISABLED, got %d", state)
	}
}

func TestConfigurableDevice_GetNvLinkErrorCounter(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	// Error counter should always return 0
	val, ret := cd.GetNvLinkErrorCounter(0, 0)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetNvLinkErrorCounter failed: %v", ret)
	}
	if val != 0 {
		t.Errorf("Expected 0, got %d", val)
	}
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	pci, ret := cd.GetNvLinkRemotePciInfo(0)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetNvLinkRemotePciInfo failed: %v", ret)
	}
	if pci.Bus != 0x3B {
		t.Errorf("Expected bus 0x3B, got 0x%X", pci.Bus)
	}
}

// =============================================================================
// Thermal Tests
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	temp, ret := cd.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SHUTDOWN)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetTemperatureThreshold(SHUTDOWN) failed: %v", ret)
	}
	if temp != 95 {
		t.Errorf("Expected 95, got %d", temp)
	}

	temp, ret = cd.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SLOWDOWN)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetTemperatureThreshold(SLOWDOWN) failed: %v", ret)
	}
	if temp != 90 {
		t.Errorf("Expected 90, got %d", temp)
	}
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	settings, ret := cd.GetThermalSettings(0)
	if ret != nvml.SUCCESS {
		t.Fatalf("GetThermalSettings failed: %v", ret)
	}
	if settings.Count != 1 {
		t.Errorf("Expected 1 sensor, got %d", settings.Count)
	}
}

// =============================================================================
// Power Tests
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	limit, ret := cd.GetEnforcedPowerLimit()
	if ret != nvml.SUCCESS {
		t.Fatalf("GetEnforcedPowerLimit failed: %v", ret)
	}
	if limit != 300000 {
		t.Errorf("Expected 300000, got %d", limit)
	}
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
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	mode, ret := cd.GetPowerManagementMode()
	if ret != nvml.SUCCESS {
		t.Fatalf("GetPowerManagementMode failed: %v", ret)
	}
	if mode != nvml.FEATURE_ENABLED {
		t.Errorf("Expected FEATURE_ENABLED, got %d", mode)
	}
}

func TestConfigurableDevice_GetPowerManagementMode_Default(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	cd, ok := dev.(*ConfigurableDevice)
	if !ok {
		t.Fatal("Expected ConfigurableDevice type")
	}

	mode, ret := cd.GetPowerManagementMode()
	if ret != nvml.SUCCESS {
		t.Fatalf("GetPowerManagementMode failed: %v", ret)
	}
	if mode != nvml.FEATURE_DISABLED {
		t.Errorf("Expected FEATURE_DISABLED, got %d", mode)
	}
}
