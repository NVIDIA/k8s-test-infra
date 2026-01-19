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

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	"k8s.io/klog/v2"
)

// MaxDevices is the maximum number of devices supported by the mock server.
// This matches the DGX A100 configuration from the upstream mock.
const MaxDevices = 8

// DefaultBAR1SizeMB is the simulated BAR1 aperture size in megabytes.
// 256 MB is typical for A100 GPUs.
const DefaultBAR1SizeMB = 256

// EnhancedDevice wraps dgxa100.Device and adds missing functionality
// without modifying the upstream mock.
type EnhancedDevice struct {
	*dgxa100.Device
}

// GetBAR1MemoryInfo returns BAR1 memory information.
// This is not implemented in dgxa100 by default.
func (d *EnhancedDevice) GetBAR1MemoryInfo() (nvml.BAR1Memory, nvml.Return) {
	bar1Bytes := uint64(DefaultBAR1SizeMB * 1024 * 1024)
	return nvml.BAR1Memory{
		Bar1Total: bar1Bytes,
		Bar1Free:  bar1Bytes,
		Bar1Used:  0,
	}, nvml.SUCCESS
}

// GetComputeRunningProcesses returns running compute processes.
// Mock returns empty list.
func (d *EnhancedDevice) GetComputeRunningProcesses() ([]nvml.ProcessInfo, nvml.Return) {
	return []nvml.ProcessInfo{}, nvml.SUCCESS
}

// GetGraphicsRunningProcesses returns running graphics processes.
// Mock returns empty list.
func (d *EnhancedDevice) GetGraphicsRunningProcesses() ([]nvml.ProcessInfo, nvml.Return) {
	return []nvml.ProcessInfo{}, nvml.SUCCESS
}

// GetPciInfo returns PCI information for the device.
func (d *EnhancedDevice) GetPciInfo() (nvml.PciInfo, nvml.Return) {
	// Parse PciBusID string (format: "0000:00:00.0")
	var domain, bus, device, function uint32
	n, err := fmt.Sscanf(d.PciBusID, "%x:%x:%x.%x", &domain, &bus, &device, &function)
	if err != nil || n != 4 {
		klog.ErrorS(err, "Failed to parse PCI bus ID",
			"busId", d.PciBusID, "parsed", n)
		return nvml.PciInfo{}, nvml.ERROR_UNKNOWN
	}

	info := nvml.PciInfo{
		Domain:         domain,
		Bus:            bus,
		Device:         device,
		PciDeviceId:    0x20B010DE, // A100 PCI Device ID
		PciSubSystemId: 0x134710DE, // A100 Subsystem ID
	}

	// Copy PciBusID to BusId field (convert string to byte array)
	for i := 0; i < len(d.PciBusID) && i < 32; i++ {
		info.BusId[i] = uint8(d.PciBusID[i])
	}

	return info, nvml.SUCCESS
}

// MockServer wraps dgxa100.Server and replaces devices with enhanced versions
type MockServer struct {
	*dgxa100.Server
	enhancedDevices [MaxDevices]*EnhancedDevice
}

// DeviceGetHandleByIndex returns an enhanced device by index
func (s *MockServer) DeviceGetHandleByIndex(index int) (nvml.Device, nvml.Return) {
	if index < 0 || index >= len(s.enhancedDevices) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	if s.enhancedDevices[index] == nil {
		return nil, nvml.ERROR_NOT_FOUND
	}
	return s.enhancedDevices[index], nvml.SUCCESS
}

// DeviceGetHandleByUUID returns an enhanced device by UUID
func (s *MockServer) DeviceGetHandleByUUID(uuid string) (nvml.Device, nvml.Return) {
	for _, dev := range s.enhancedDevices {
		if dev != nil && dev.UUID == uuid {
			return dev, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// DeviceGetHandleByPciBusId returns an enhanced device by PCI bus ID
func (s *MockServer) DeviceGetHandleByPciBusId(pciBusId string) (nvml.Device, nvml.Return) {
	for _, dev := range s.enhancedDevices {
		if dev != nil && dev.PciBusID == pciBusId {
			return dev, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// NewMockServer creates a mock server from a dgxa100 server
func NewMockServer(base *dgxa100.Server) *MockServer {
	s := &MockServer{
		Server: base,
	}

	// Wrap each device
	for i := range base.Devices {
		if dev, ok := base.Devices[i].(*dgxa100.Device); ok {
			s.enhancedDevices[i] = &EnhancedDevice{Device: dev}
		}
	}

	return s
}
