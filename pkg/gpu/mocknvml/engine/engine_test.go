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

	// LookupDevice should return nil when not initialized (engine not ready)
	// This is different from InvalidDeviceInstance which is for invalid handles in an initialized engine
	dev := e.LookupDevice(1)
	if dev != nil {
		t.Error("Expected nil when engine not initialized")
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
