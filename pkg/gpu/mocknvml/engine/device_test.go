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

func TestEnhancedDevice_GetBAR1MemoryInfo(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*EnhancedDevice)
	if !ok {
		t.Fatal("Expected EnhancedDevice type")
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

func TestEnhancedDevice_GetComputeRunningProcesses(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*EnhancedDevice)
	if !ok {
		t.Fatal("Expected EnhancedDevice type")
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

func TestEnhancedDevice_GetGraphicsRunningProcesses(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*EnhancedDevice)
	if !ok {
		t.Fatal("Expected EnhancedDevice type")
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

func TestEnhancedDevice_GetPciInfo(t *testing.T) {
	e := NewEngine(nil)
	_ = e.Init()
	defer func() { _ = e.Shutdown() }()

	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle)

	enhanced, ok := dev.(*EnhancedDevice)
	if !ok {
		t.Fatal("Expected EnhancedDevice type")
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
