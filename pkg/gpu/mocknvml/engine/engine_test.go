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
)

func TestEngine_Singleton(t *testing.T) {
	e1 := GetEngine()
	e2 := GetEngine()

	if e1 != e2 {
		t.Error("GetEngine should return same instance")
	}
}

func TestEngine_NewEngine(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}

	e := NewEngine(config)
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.config.NumDevices != 4 {
		t.Errorf("Expected NumDevices 4, got %d", e.config.NumDevices)
	}
	if e.handles == nil {
		t.Error("HandleTable not initialized")
	}
}

func TestEngine_NewEngineDefaultConfig(t *testing.T) {
	e := NewEngine(nil)
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.config == nil {
		t.Fatal("Config not initialized")
	}
	// Should use default config
	if e.config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", e.config.NumDevices)
	}
}

func TestEngine_InitShutdown(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)

	// Init
	ret := e.Init()
	if ret != nvml.SUCCESS {
		t.Errorf("Init failed with %v", ret)
	}
	if e.server == nil {
		t.Error("Server not initialized")
	}
	if e.initCount != 1 {
		t.Errorf("Expected initCount 1, got %d", e.initCount)
	}

	// Shutdown
	ret = e.Shutdown()
	if ret != nvml.SUCCESS {
		t.Errorf("Shutdown failed with %v", ret)
	}
	if e.server != nil {
		t.Error("Server should be nil after shutdown")
	}
	if e.initCount != 0 {
		t.Errorf("Expected initCount 0, got %d", e.initCount)
	}
}

func TestEngine_MultipleInit(t *testing.T) {
	config := &Config{
		NumDevices:    4,
		DriverVersion: "550.54.15",
	}
	e := NewEngine(config)

	// First init
	ret := e.Init()
	if ret != nvml.SUCCESS {
		t.Fatalf("First init failed: %v", ret)
	}

	// Second init (should succeed and increment counter)
	ret = e.Init()
	if ret != nvml.SUCCESS {
		t.Errorf("Second init failed: %v", ret)
	}
	if e.initCount != 2 {
		t.Errorf("Expected initCount 2, got %d", e.initCount)
	}

	// First shutdown (should not uninitialize)
	ret = e.Shutdown()
	if ret != nvml.SUCCESS {
		t.Errorf("First shutdown failed: %v", ret)
	}
	if e.server == nil {
		t.Error("Server should still be initialized after first shutdown")
	}
	if e.initCount != 1 {
		t.Errorf("Expected initCount 1, got %d", e.initCount)
	}

	// Second shutdown (should uninitialize)
	ret = e.Shutdown()
	if ret != nvml.SUCCESS {
		t.Errorf("Second shutdown failed: %v", ret)
	}
	if e.server != nil {
		t.Error("Server should be nil after final shutdown")
	}
	if e.initCount != 0 {
		t.Errorf("Expected initCount 0, got %d", e.initCount)
	}
}

func TestEngine_ShutdownWithoutInit(t *testing.T) {
	e := NewEngine(nil)

	ret := e.Shutdown()
	if ret != nvml.ERROR_UNINITIALIZED {
		t.Errorf("Expected ERROR_UNINITIALIZED, got %v", ret)
	}
}

func TestEngine_DeviceGetCountBeforeInit(t *testing.T) {
	e := NewEngine(nil)

	_, ret := e.DeviceGetCount()
	if ret != nvml.ERROR_UNINITIALIZED {
		t.Errorf("Expected ERROR_UNINITIALIZED, got %v", ret)
	}
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
	if ret != nvml.SUCCESS {
		t.Errorf("DeviceGetCount failed: %v", ret)
	}
	if count != 4 {
		t.Errorf("Expected count 4, got %d", count)
	}
}

func TestEngine_DeviceGetCountDefault(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	count, ret := e.DeviceGetCount()
	if ret != nvml.SUCCESS {
		t.Errorf("DeviceGetCount failed: %v", ret)
	}
	if count != 8 {
		t.Errorf("Expected default count 8, got %d", count)
	}
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
	if ret != nvml.SUCCESS {
		t.Errorf("DeviceGetHandleByIndex failed: %v", ret)
	}
	if handle == 0 {
		t.Error("Expected non-zero handle")
	}

	// Same index should return same handle
	handle2, ret := e.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		t.Errorf("Second DeviceGetHandleByIndex failed: %v", ret)
	}
	if handle != handle2 {
		t.Error("Expected same handle for same index")
	}
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
	if ret == nvml.SUCCESS {
		t.Error("Expected error for invalid index")
	}
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
	if dev == nil {
		t.Error("LookupDevice returned nil for valid handle")
	}

	// Invalid handle - returns InvalidDeviceInstance (null-object pattern)
	invalidDev := e.LookupDevice(999)
	if invalidDev != InvalidDeviceInstance {
		t.Error("Expected InvalidDeviceInstance for invalid handle")
	}
}

func TestEngine_LookupDeviceBeforeInit(t *testing.T) {
	e := NewEngine(nil)
	dev := e.LookupDevice(1)
	if dev == nil {
		t.Fatal("LookupDevice on uninitialized engine returned nil, expected InvalidDeviceInstance")
	}
	if dev != InvalidDeviceInstance {
		t.Errorf("Expected InvalidDeviceInstance, got %T", dev)
	}
	_, ret := dev.GetName()
	if ret != nvml.ERROR_INVALID_ARGUMENT {
		t.Errorf("Expected ERROR_INVALID_ARGUMENT from InvalidDeviceInstance, got %v", ret)
	}
}

func TestEngine_LookupConfigurableDevice_UninitializedReturnsNil(t *testing.T) {
	e := NewEngine(nil)
	dev := e.LookupConfigurableDevice(0x1234)
	if dev != nil {
		t.Error("LookupConfigurableDevice on uninitialized engine should return nil")
	}
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
	if ret != nvml.SUCCESS {
		t.Errorf("SystemGetDriverVersion failed: %v", ret)
	}
	if version != customVersion {
		t.Errorf("Expected driver version %s, got %s", customVersion, version)
	}
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

	if e.handles.Count() == 0 {
		t.Error("Expected handles to be registered")
	}

	_ = e.Shutdown()

	if e.handles.Count() != 0 {
		t.Error("Expected handles to be cleared on shutdown")
	}
}

func TestEngine_ConfigFromEnvironment(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "6")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "600.00.00")

	e := NewEngine(nil) // Should load from env
	if e.config.NumDevices != 6 {
		t.Errorf("Expected NumDevices 6 from env, got %d", e.config.NumDevices)
	}
	if e.config.DriverVersion != "600.00.00" {
		t.Errorf("Expected DriverVersion from env, got %s", e.config.DriverVersion)
	}
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
	if ret != nvml.SUCCESS {
		t.Errorf("DeviceGetHandleByUUID failed: %v", ret)
	}
	if handleByUUID != handle {
		t.Error("Expected same handle for same device")
	}
}

func TestEngine_DeviceGetHandleByUUIDInvalid(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	_, ret := e.DeviceGetHandleByUUID("invalid-uuid-12345")
	if ret == nvml.SUCCESS {
		t.Error("Expected error for invalid UUID")
	}
}

func TestEngine_DeviceGetHandleByPciBusId(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get first device's PCI bus ID
	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)
	pciInfo, ret := dev.GetPciInfo()
	if ret != nvml.SUCCESS {
		t.Fatalf("GetPciInfo failed: %v", ret)
	}

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
	if ret != nvml.SUCCESS {
		t.Errorf("DeviceGetHandleByPciBusId failed: %v", ret)
	}
	if handleByPCI != handle {
		t.Error("Expected same handle for same device")
	}
}

func TestEngine_DeviceGetHandleByPciBusIdInvalid(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	_, ret := e.DeviceGetHandleByPciBusId("0000:FF:FF.0")
	if ret == nvml.SUCCESS {
		t.Error("Expected error for invalid PCI bus ID")
	}
}

// --- Visibility filtering tests ---

// TestDetectVisibleDevices_NonePresent verifies nil is returned when no device
// nodes exist (typical host/driver-plugin context).
func TestDetectVisibleDevices_NonePresent(t *testing.T) {
	dir := t.TempDir()
	// No files created – simulates no /dev/nvidia* nodes
	result := detectVisibleDevicesAt(dir+"/nvidia%d", 4)
	if result != nil {
		t.Errorf("Expected nil (no filtering) when no nodes exist, got %v", result)
	}
}

// TestDetectVisibleDevices_AllPresent verifies nil is returned when every
// device node exists (no filtering needed).
func TestDetectVisibleDevices_AllPresent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		f, err := os.Create(fmt.Sprintf("%s/nvidia%d", dir, i))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	result := detectVisibleDevicesAt(dir+"/nvidia%d", 4)
	if result != nil {
		t.Errorf("Expected nil (no filtering) when all nodes exist, got %v", result)
	}
}

// TestDetectVisibleDevices_Subset verifies correct filtering when only some
// device nodes exist (e.g. container with CDI-injected subset).
func TestDetectVisibleDevices_Subset(t *testing.T) {
	dir := t.TempDir()
	// Create only nodes 0 and 2 out of 4
	for _, idx := range []int{0, 2} {
		f, err := os.Create(fmt.Sprintf("%s/nvidia%d", dir, idx))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	result := detectVisibleDevicesAt(dir+"/nvidia%d", 4)
	if len(result) != 2 {
		t.Fatalf("Expected 2 visible devices, got %d: %v", len(result), result)
	}
	if result[0] != 0 || result[1] != 2 {
		t.Errorf("Expected visible devices [0 2], got %v", result)
	}
}

// TestVisibility_DeviceGetCount verifies that DeviceGetCount returns the
// filtered count when visibility filtering is active.
func TestVisibility_DeviceGetCount(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.54.15"})
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Before filtering: should see all 4
	count, ret := e.DeviceGetCount()
	if ret != nvml.SUCCESS {
		t.Fatalf("DeviceGetCount failed: %v", ret)
	}
	if count != 4 {
		t.Errorf("Expected 4, got %d", count)
	}

	// Activate filtering: only devices 1 and 3 visible
	e.SetVisibleDevicesForTesting([]int{1, 3})
	count, ret = e.DeviceGetCount()
	if ret != nvml.SUCCESS {
		t.Fatalf("DeviceGetCount failed: %v", ret)
	}
	if count != 2 {
		t.Errorf("Expected 2 visible devices, got %d", count)
	}

	// Disable filtering
	e.SetVisibleDevicesForTesting(nil)
	count, ret = e.DeviceGetCount()
	if ret != nvml.SUCCESS {
		t.Fatalf("DeviceGetCount failed: %v", ret)
	}
	if count != 4 {
		t.Errorf("Expected 4 after disabling filter, got %d", count)
	}
}

// TestVisibility_DeviceGetHandleByIndex verifies index remapping through the
// visibility table.
func TestVisibility_DeviceGetHandleByIndex(t *testing.T) {
	e := NewEngine(&Config{NumDevices: 4, DriverVersion: "550.54.15"})
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	// Get the actual device at index 2 before filtering
	handleDev2, ret := e.DeviceGetHandleByIndex(2)
	if ret != nvml.SUCCESS {
		t.Fatalf("DeviceGetHandleByIndex(2) failed: %v", ret)
	}

	// Activate filtering: visible = [2, 3] (device 2 becomes visible index 0)
	e.SetVisibleDevicesForTesting([]int{2, 3})

	// Visible index 0 should map to actual device 2
	handle, ret := e.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		t.Fatalf("DeviceGetHandleByIndex(0) with visibility failed: %v", ret)
	}
	if handle != handleDev2 {
		t.Error("Visible index 0 should map to actual device 2")
	}

	// Visible index 2 should be out of range (only 2 visible)
	_, ret = e.DeviceGetHandleByIndex(2)
	if ret != nvml.ERROR_INVALID_ARGUMENT {
		t.Errorf("Expected ERROR_INVALID_ARGUMENT for out-of-range visible index, got %v", ret)
	}
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
	if ret != nvml.SUCCESS {
		t.Errorf("Expected SUCCESS for visible device UUID, got %v", ret)
	}

	// Device 1's UUID should NOT resolve
	_, ret = e.DeviceGetHandleByUUID(uuid1)
	if ret != nvml.ERROR_NOT_FOUND {
		t.Errorf("Expected ERROR_NOT_FOUND for non-visible device UUID, got %v", ret)
	}
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
	if ret != nvml.SUCCESS {
		t.Errorf("Expected SUCCESS for visible device PCI bus ID, got %v", ret)
	}

	// Device 1's PCI bus ID should NOT resolve
	_, ret = e.DeviceGetHandleByPciBusId(busId1)
	if ret != nvml.ERROR_NOT_FOUND {
		t.Errorf("Expected ERROR_NOT_FOUND for non-visible device PCI bus ID, got %v", ret)
	}
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
