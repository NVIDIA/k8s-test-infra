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
	"fmt"
	"os"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestEngine_Singleton(t *testing.T) {
	e1 := GetEngine()
	e2 := GetEngine()

	require.Same(t, e1, e2, "GetEngine should return same instance")
}

func TestEngine_NewEngine(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}

	e := NewEngine(config)
	require.NotNil(t, e, "NewEngine returned nil")
	require.Equal(t, 4, e.config.NumDevices, "Expected NumDevices 4")
	require.NotNil(t, e.handles, "HandleTable not initialized")
}

func TestEngine_NewEngineDefaultConfig(t *testing.T) {
	e := NewEngine(nil)
	require.NotNil(t, e, "NewEngine returned nil")
	require.NotNil(t, e.config, "Config not initialized")
	// Should use default config
	require.Equal(t, 8, e.config.NumDevices, "Expected default NumDevices 8")
}

func TestEngine_InitShutdown(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)

	// Init
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "Init failed")
	require.NotNil(t, e.server, "Server not initialized")
	require.Equal(t, 1, e.initCount, "Expected initCount 1")

	// Shutdown
	ret = e.Shutdown()
	require.Equal(t, nvml.SUCCESS, ret, "Shutdown failed")
	require.Nil(t, e.server, "Server should be nil after shutdown")
	require.Equal(t, 0, e.initCount, "Expected initCount 0")
}

func TestEngine_MultipleInit(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)

	// First init
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "First init failed")

	// Second init (should succeed and increment counter)
	ret = e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "Second init failed")
	require.Equal(t, 2, e.initCount, "Expected initCount 2")

	// First shutdown (should not uninitialize)
	ret = e.Shutdown()
	require.Equal(t, nvml.SUCCESS, ret, "First shutdown failed")
	require.NotNil(t, e.server, "Server should still be initialized after first shutdown")
	require.Equal(t, 1, e.initCount, "Expected initCount 1")

	// Second shutdown (should uninitialize)
	ret = e.Shutdown()
	require.Equal(t, nvml.SUCCESS, ret, "Second shutdown failed")
	require.Nil(t, e.server, "Server should be nil after final shutdown")
	require.Equal(t, 0, e.initCount, "Expected initCount 0")
}

func TestEngine_ShutdownWithoutInit(t *testing.T) {
	e := NewEngine(nil)

	ret := e.Shutdown()
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret, "Expected ERROR_UNINITIALIZED")
}

func TestEngine_DeviceGetCountBeforeInit(t *testing.T) {
	e := NewEngine(nil)

	_, ret := e.DeviceGetCount()
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret, "Expected ERROR_UNINITIALIZED")
}

func TestEngine_DeviceGetCount(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	count, ret := e.DeviceGetCount()
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetCount failed")
	require.Equal(t, 4, count, "Expected count 4")
}

func TestEngine_DeviceGetCountDefault(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	count, ret := e.DeviceGetCount()
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetCount failed")
	require.Equal(t, 8, count, "Expected default count 8")
}

func TestEngine_DeviceGetHandleByIndex(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Valid index
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetHandleByIndex failed")
	require.NotZero(t, handle, "Expected non-zero handle")

	// Same index should return same handle
	handle2, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret, "Second DeviceGetHandleByIndex failed")
	require.Equal(t, handle, handle2, "Expected same handle for same index")
}

func TestEngine_DeviceGetHandleByIndexInvalid(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Invalid index (out of range)
	_, ret := e.DeviceGetHandleByIndex(10)
	require.NotEqual(t, nvml.SUCCESS, ret, "Expected error for invalid index")
}

func TestEngine_LookupDevice(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)
	require.NotNil(t, dev, "LookupDevice returned nil for valid handle")

	// Invalid handle - returns InvalidDeviceInstance (null-object pattern)
	invalidDev := e.LookupDevice(999)
	require.Equal(t, InvalidDeviceInstance, invalidDev, "Expected InvalidDeviceInstance for invalid handle")
}

func TestEngine_LookupDeviceBeforeInit(t *testing.T) {
	e := NewEngine(nil)
	dev := e.LookupDevice(1)
	require.NotNil(t, dev, "LookupDevice on uninitialized engine returned nil, expected InvalidDeviceInstance")
	require.Equal(t, InvalidDeviceInstance, dev, "Expected InvalidDeviceInstance")
	_, ret := dev.GetName()
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "Expected ERROR_INVALID_ARGUMENT from InvalidDeviceInstance")
}

func TestEngine_LookupConfigurableDevice_UninitializedReturnsNil(t *testing.T) {
	e := NewEngine(nil)
	dev := e.LookupConfigurableDevice(0x1234)
	require.Nil(t, dev, "LookupConfigurableDevice on uninitialized engine should return nil")
}

func TestEngine_DriverVersionOverride(t *testing.T) {
	customVersion := "999.99.99"
	config := &Config{
		NumDevices:    4,
		DriverVersion: customVersion,
	}
	e := NewEngine(config)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	version, ret := e.server.SystemGetDriverVersion()
	require.Equal(t, nvml.SUCCESS, ret, "SystemGetDriverVersion failed")
	require.Equal(t, customVersion, version, "Expected driver version %s", customVersion)
}

func TestEngine_HandleTableCleanupOnShutdown(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)
	_ = e.Init()

	// Get some handles
	_, _ = e.DeviceGetHandleByIndex(0)
	_, _ = e.DeviceGetHandleByIndex(1)

	require.NotZero(t, e.handles.Count(), "Expected handles to be registered")

	_ = e.Shutdown()

	require.Zero(t, e.handles.Count(), "Expected handles to be cleared on shutdown")
}

func TestEngine_ConfigFromEnvironment(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "6")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "600.00.00")

	e := NewEngine(nil) // Should load from env
	require.Equal(t, 6, e.config.NumDevices, "Expected NumDevices 6 from env")
	require.Equal(t, "600.00.00", e.config.DriverVersion, "Expected DriverVersion from env")
}

func TestEngine_DeviceGetHandleByUUID(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get first device's UUID
	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)
	uuid, _ := dev.GetUUID()

	// Lookup by UUID
	handleByUUID, ret := e.DeviceGetHandleByUUID(uuid)
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetHandleByUUID failed")
	require.Equal(t, handle, handleByUUID, "Expected same handle for same device")
}

func TestEngine_DeviceGetHandleByUUIDInvalid(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	_, ret := e.DeviceGetHandleByUUID("invalid-uuid-12345")
	require.NotEqual(t, nvml.SUCCESS, ret, "Expected error for invalid UUID")
}

func TestEngine_DeviceGetHandleByPciBusId(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get first device's PCI bus ID
	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)
	pciInfo, ret := dev.GetPciInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetPciInfo failed")

	// Convert BusId to string (trim null bytes)
	var busId string
	for _, b := range pciInfo.BusId {
		if b == 0 {
			break
		}
		busId += string(rune(b))
	}

	// Lookup by PCI bus ID
	handleByPCI, ret := e.DeviceGetHandleByPciBusId(busId)
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetHandleByPciBusId failed")
	require.Equal(t, handle, handleByPCI, "Expected same handle for same device")
}

func TestEngine_DeviceGetHandleByPciBusIdInvalid(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	_, ret := e.DeviceGetHandleByPciBusId("0000:FF:FF.0")
	require.NotEqual(t, nvml.SUCCESS, ret, "Expected error for invalid PCI bus ID")
}

// --- Visibility filtering tests ---

// TestDetectVisibleDevices_NonePresent verifies nil is returned when no device
// nodes exist (typical host/driver-plugin context).
func TestDetectVisibleDevices_NonePresent(t *testing.T) {
	dir := t.TempDir()
	// No files created – simulates no /dev/nvidia* nodes
	result := detectVisibleDevicesAt(dir+"/nvidia%d", 4)
	require.Nil(t, result, "Expected nil (no filtering) when no nodes exist")
}

// TestDetectVisibleDevices_AllPresent verifies nil is returned when every
// device node exists (no filtering needed).
func TestDetectVisibleDevices_AllPresent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		f, err := os.Create(fmt.Sprintf("%s/nvidia%d", dir, i))
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}

	result := detectVisibleDevicesAt(dir+"/nvidia%d", 4)
	require.Nil(t, result, "Expected nil (no filtering) when all nodes exist")
}

// TestDetectVisibleDevices_Subset verifies correct filtering when only some
// device nodes exist (e.g. container with CDI-injected subset).
func TestDetectVisibleDevices_Subset(t *testing.T) {
	dir := t.TempDir()
	// Create only nodes 0 and 2 out of 4
	for _, idx := range []int{0, 2} {
		f, err := os.Create(fmt.Sprintf("%s/nvidia%d", dir, idx))
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}

	result := detectVisibleDevicesAt(dir+"/nvidia%d", 4)
	require.Len(t, result, 2, "Expected 2 visible devices")
	require.Equal(t, []int{0, 2}, result, "Expected visible devices [0 2]")
}

// TestVisibility_DeviceGetCount verifies that DeviceGetCount returns the
// filtered count when visibility filtering is active.
func TestVisibility_DeviceGetCount(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.54.15"})
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Before filtering: should see all 4
	count, ret := e.DeviceGetCount()
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetCount failed")
	require.Equal(t, 4, count, "Expected 4")

	// Activate filtering: only devices 1 and 3 visible
	e.SetVisibleDevicesForTesting([]int{1, 3})
	count, ret = e.DeviceGetCount()
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetCount failed")
	require.Equal(t, 2, count, "Expected 2 visible devices")

	// Disable filtering
	e.SetVisibleDevicesForTesting(nil)
	count, ret = e.DeviceGetCount()
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetCount failed")
	require.Equal(t, 4, count, "Expected 4 after disabling filter")
}

// TestVisibility_DeviceGetHandleByIndex verifies index remapping through the
// visibility table.
func TestVisibility_DeviceGetHandleByIndex(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.54.15"})
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get the actual device at index 2 before filtering
	handleDev2, ret := e.DeviceGetHandleByIndex(2)
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetHandleByIndex(2) failed")

	// Activate filtering: visible = [2, 3] (device 2 becomes visible index 0)
	e.SetVisibleDevicesForTesting([]int{2, 3})

	// Visible index 0 should map to actual device 2
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret, "DeviceGetHandleByIndex(0) with visibility failed")
	require.Equal(t, handleDev2, handle, "Visible index 0 should map to actual device 2")

	// Visible index 2 should be out of range (only 2 visible)
	_, ret = e.DeviceGetHandleByIndex(2)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "Expected ERROR_INVALID_ARGUMENT for out-of-range visible index")
}

// TestVisibility_DeviceGetHandleByUUID verifies that UUID lookups respect
// device visibility filtering.
func TestVisibility_DeviceGetHandleByUUID(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.54.15"})
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get UUIDs for devices 0 and 1
	h0, _ := e.DeviceGetHandleByIndex(0)
	uuid0, _ := e.LookupDevice(h0).GetUUID()
	h1, _ := e.DeviceGetHandleByIndex(1)
	uuid1, _ := e.LookupDevice(h1).GetUUID()

	// Only device 0 is visible
	e.SetVisibleDevicesForTesting([]int{0})

	// Device 0's UUID should resolve
	_, ret := e.DeviceGetHandleByUUID(uuid0)
	require.Equal(t, nvml.SUCCESS, ret, "Expected SUCCESS for visible device UUID")

	// Device 1's UUID should NOT resolve
	_, ret = e.DeviceGetHandleByUUID(uuid1)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "Expected ERROR_NOT_FOUND for non-visible device UUID")
}

// TestVisibility_DeviceGetHandleByPciBusId verifies that PCI bus ID lookups
// respect device visibility filtering.
func TestVisibility_DeviceGetHandleByPciBusId(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.54.15"})
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get PCI bus IDs for devices 0 and 1
	h0, _ := e.DeviceGetHandleByIndex(0)
	pci0, _ := e.LookupDevice(h0).GetPciInfo()
	busId0 := pciInfoBusIdString(pci0)

	h1, _ := e.DeviceGetHandleByIndex(1)
	pci1, _ := e.LookupDevice(h1).GetPciInfo()
	busId1 := pciInfoBusIdString(pci1)

	// Only device 0 is visible
	e.SetVisibleDevicesForTesting([]int{0})

	// Device 0's PCI bus ID should resolve
	_, ret := e.DeviceGetHandleByPciBusId(busId0)
	require.Equal(t, nvml.SUCCESS, ret, "Expected SUCCESS for visible device PCI bus ID")

	// Device 1's PCI bus ID should NOT resolve
	_, ret = e.DeviceGetHandleByPciBusId(busId1)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "Expected ERROR_NOT_FOUND for non-visible device PCI bus ID")
}

// pciInfoBusIdString extracts a Go string from the null-terminated BusId array.
func pciInfoBusIdString(pci nvml.PciInfo) string {
	var s string
	for _, b := range pci.BusId {
		if b == 0 {
			break
		}
		s += string(rune(b))
	}
	return s
}
