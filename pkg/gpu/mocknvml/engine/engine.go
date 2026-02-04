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
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
)

// Engine manages the mock NVML lifecycle and handle mapping.
// It does NOT implement nvml.Interface - it delegates to MockServer
// which wraps dgxa100.Server (the actual nvml.Interface implementation).
type Engine struct {
	server    *MockServer
	config    *Config
	handles   *HandleTable
	initCount int
	mu        sync.RWMutex
}

var (
	engineInstance *Engine
	engineOnce     sync.Once
)

// GetEngine returns the singleton Engine instance.
func GetEngine() *Engine {
	engineOnce.Do(func() {
		engineInstance = NewEngine(nil)
	})
	return engineInstance
}

// NewEngine creates a new Engine with optional configuration.
// If config is nil, it loads from environment.
// The server is created lazily on first Init() call.
func NewEngine(config *Config) *Engine {
	if config == nil {
		config = LoadConfig()
	}

	e := &Engine{
		config:  config,
		handles: NewHandleTable(),
	}

	return e
}

// Init initializes the NVML library.
// On first call, creates and configures the mock server.
func (e *Engine) Init() nvml.Return {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount > 0 {
		e.initCount++
		return nvml.SUCCESS
	}

	// Create server on first init
	server, err := e.createServer()
	if err != nil {
		debugLog("[ENGINE] Failed to create mock server: %v\n", err)
		return nvml.ERROR_UNKNOWN
	}

	e.server = server
	e.initCount = 1
	debugLog("[ENGINE] Initialized with %d devices\n", e.config.NumDevices)
	return nvml.SUCCESS
}

// createServer creates and configures the mock server based on configuration
func (e *Engine) createServer() (*MockServer, error) {
	// Create base dgxa100 mock server
	base := dgxa100.New()

	// Create our mock server wrapper
	server := &MockServer{
		Server: base,
	}

	// Create devices based on configuration type
	if e.config.YAMLConfig != nil {
		// YAML-based configuration
		e.createDevicesFromYAML(server, base)
	} else {
		// Legacy environment variable configuration
		e.createDefaultDevices(server, base)
	}

	// Apply system-level configuration
	e.applySystemConfig(server)

	return server, nil
}

// createDevicesFromYAML creates ConfigurableDevices from YAML configuration
func (e *Engine) createDevicesFromYAML(server *MockServer, base *dgxa100.Server) {
	debugLog("[ENGINE] Creating devices from YAML config\n")

	for i := 0; i < e.config.NumDevices && i < MaxDevices; i++ {
		// Get merged device config (defaults + overrides)
		deviceCfg := e.config.GetDeviceConfig(i)

		// Get per-device values
		uuid := e.config.GetDeviceUUID(i)
		pciBusID := e.config.GetDevicePCIBusID(i)
		minorNumber := e.config.GetDeviceMinorNumber(i)

		// Get base device from dgxa100
		baseDevice, ok := base.Devices[i].(*dgxa100.Device)
		if !ok {
			debugLog("[ENGINE] Device %d is not dgxa100.Device type, skipping\n", i)
			continue
		}

		// Create configurable device
		server.configurableDevices[i] = NewConfigurableDevice(
			i,
			baseDevice,
			deviceCfg,
			uuid,
			pciBusID,
			minorNumber,
		)

		debugLog("[ENGINE] Created device %d: uuid=%s pci=%s\n", i, uuid, pciBusID)
	}
}

// createDefaultDevices creates devices with default/env configuration (legacy mode)
func (e *Engine) createDefaultDevices(server *MockServer, base *dgxa100.Server) {
	debugLog("[ENGINE] Creating devices with default config\n")

	for i := 0; i < e.config.NumDevices && i < MaxDevices; i++ {
		// Get base device from dgxa100
		baseDevice, ok := base.Devices[i].(*dgxa100.Device)
		if !ok {
			debugLog("[ENGINE] Device %d is not dgxa100.Device type, skipping\n", i)
			continue
		}

		// Create configurable device with nil config (uses defaults)
		server.configurableDevices[i] = NewConfigurableDevice(
			i,
			baseDevice,
			nil, // No YAML config
			"",  // Use default UUID
			"",  // Use default PCI bus ID
			i,   // Minor number = index
		)
	}
}

// applySystemConfig applies system-level configuration
func (e *Engine) applySystemConfig(server *MockServer) {
	// Override device count
	numDevices := e.config.NumDevices
	server.DeviceGetCountFunc = func() (int, nvml.Return) {
		if numDevices <= MaxDevices {
			return numDevices, nvml.SUCCESS
		}
		return MaxDevices, nvml.SUCCESS
	}

	// Override driver version
	if e.config.DriverVersion != "" {
		driverVersion := e.config.DriverVersion
		server.SystemGetDriverVersionFunc = func() (string, nvml.Return) {
			return driverVersion, nvml.SUCCESS
		}
	}

	// If YAML config, apply additional system settings
	if e.config.YAMLConfig != nil {
		yamlCfg := e.config.YAMLConfig

		// NVML version
		if yamlCfg.System.NVMLVersion != "" {
			nvmlVersion := yamlCfg.System.NVMLVersion
			server.SystemGetNVMLVersionFunc = func() (string, nvml.Return) {
				return nvmlVersion, nvml.SUCCESS
			}
		}

		// CUDA version (as integer: major*1000 + minor*10)
		if yamlCfg.System.CUDAVersionMajor > 0 {
			cudaVersion := yamlCfg.System.CUDAVersionMajor*1000 + yamlCfg.System.CUDAVersionMinor*10
			server.SystemGetCudaDriverVersionFunc = func() (int, nvml.Return) {
				return cudaVersion, nvml.SUCCESS
			}
		}
	}
}

// Shutdown shuts down the NVML library.
func (e *Engine) Shutdown() nvml.Return {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount == 0 {
		return nvml.ERROR_UNINITIALIZED
	}

	e.initCount--
	if e.initCount > 0 {
		return nvml.SUCCESS
	}

	e.server = nil
	e.handles.Clear()

	debugLog("[ENGINE] Shutdown complete\n")
	return nvml.SUCCESS
}

// DeviceGetCount returns the number of compute devices.
func (e *Engine) DeviceGetCount() (int, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	return e.server.DeviceGetCount()
}

// DeviceGetHandleByIndex returns a handle for the device at the given index.
func (e *Engine) DeviceGetHandleByIndex(index int) (uintptr, nvml.Return) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount == 0 {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	device, ret := e.server.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	// Register in handle table
	handle := e.handles.Register(device)
	return handle, nvml.SUCCESS
}

// DeviceGetHandleByUUID returns a handle for the device with the given UUID.
func (e *Engine) DeviceGetHandleByUUID(uuid string) (uintptr, nvml.Return) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount == 0 {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	device, ret := e.server.DeviceGetHandleByUUID(uuid)
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	handle := e.handles.Register(device)
	return handle, nvml.SUCCESS
}

// DeviceGetHandleByPciBusId returns a handle for the device with the given PCI Bus ID.
func (e *Engine) DeviceGetHandleByPciBusId(pciBusId string) (uintptr, nvml.Return) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount == 0 {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	device, ret := e.server.DeviceGetHandleByPciBusId(pciBusId)
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	handle := e.handles.Register(device)
	return handle, nvml.SUCCESS
}

// LookupDevice returns the device object for a given handle.
// Returns nil if the engine is not initialized or the handle is invalid.
func (e *Engine) LookupDevice(handle uintptr) nvml.Device {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return nil
	}
	return e.handles.Lookup(handle)
}

// LookupConfigurableDevice returns the ConfigurableDevice for a given handle.
// This is useful when we need access to ConfigurableDevice-specific methods.
func (e *Engine) LookupConfigurableDevice(handle uintptr) *ConfigurableDevice {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return nil
	}

	device := e.handles.Lookup(handle)
	if device == nil {
		return nil
	}

	// Type assert to ConfigurableDevice
	if cd, ok := device.(*ConfigurableDevice); ok {
		return cd
	}
	return nil
}

// SystemGetDriverVersion returns the NVIDIA driver version.
// This function can be called before initialization (returns config version).
func (e *Engine) SystemGetDriverVersion() (string, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Return config version even before init (nvidia-smi may call this early)
	if e.initCount == 0 {
		return e.config.DriverVersion, nvml.SUCCESS
	}

	return e.server.SystemGetDriverVersion()
}

// SystemGetNVMLVersion returns the NVML version string.
// This function can be called before initialization (returns config version).
func (e *Engine) SystemGetNVMLVersion() (string, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Return config version even before init (nvidia-smi may call this early)
	if e.initCount == 0 {
		if e.config.YAMLConfig != nil && e.config.YAMLConfig.System.NVMLVersion != "" {
			return e.config.YAMLConfig.System.NVMLVersion, nvml.SUCCESS
		}
		// Default NVML version based on driver
		return "12." + e.config.DriverVersion, nvml.SUCCESS
	}

	return e.server.SystemGetNVMLVersion()
}

// SystemGetCudaDriverVersion returns the CUDA driver version.
// This function can be called before initialization (returns config version).
func (e *Engine) SystemGetCudaDriverVersion() (int, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Return config version even before init (nvidia-smi may call this early)
	if e.initCount == 0 {
		if e.config.YAMLConfig != nil && e.config.YAMLConfig.System.CUDAVersionMajor > 0 {
			cudaVersion := e.config.YAMLConfig.System.CUDAVersionMajor*1000 + e.config.YAMLConfig.System.CUDAVersionMinor*10
			return cudaVersion, nvml.SUCCESS
		}
		// Default CUDA version
		return 12040, nvml.SUCCESS
	}

	return e.server.SystemGetCudaDriverVersion()
}

// GetConfig returns the engine configuration (for testing)
func (e *Engine) GetConfig() *Config {
	return e.config
}

// ResetForTesting resets the engine singleton for testing purposes.
// This clears all cached state including config cache.
// WARNING: Only use in tests! Not thread-safe during concurrent access.
func ResetForTesting() {
	// Clear config cache first (before engine reset)
	ClearConfigCache()

	// Reset engine singleton
	engineOnce = sync.Once{}
	engineInstance = nil

	debugLog("[ENGINE] Reset for testing\n")
}
