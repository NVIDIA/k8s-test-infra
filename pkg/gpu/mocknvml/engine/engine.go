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

// Engine manages the mock NVML implementation and provides a bridge between
// C and Go NVML interfaces.
type Engine struct {
	server      nvml.Interface
	config      *Config
	handles     *HandleTable
	initialized bool
	initCount   int
	mu          sync.RWMutex
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
// If config is nil, default configuration is used.
func NewEngine(config *Config) *Engine {
	if config == nil {
		config = DefaultConfig()
	}

	// Create base mock server
	server := dgxa100.New()

	// Apply configuration customizations
	if config.NumDevices != 8 {
		originalCount := server.DeviceGetCountFunc
		server.DeviceGetCountFunc = func() (int, nvml.Return) {
			if config.NumDevices > 0 {
				return config.NumDevices, nvml.SUCCESS
			}
			return originalCount()
		}
	}

	if config.DriverVersion != "" {
		server.SystemGetDriverVersionFunc = func() (string, nvml.Return) {
			return config.DriverVersion, nvml.SUCCESS
		}
	}

	// Add missing function implementations to all devices
	for i := range server.Devices {
		dev, ok := server.Devices[i].(*dgxa100.Device)
		if !ok {
			continue
		}

		// Critical: Set function fields directly on dev, not in closures
		// that would capture the loop variable. These particular closures
		// don't reference 'dev', but it's good practice to be explicit.

		// Add BAR1 memory info (not implemented in dgxa100 by default)
		dev.GetBAR1MemoryInfoFunc = func() (nvml.BAR1Memory, nvml.Return) {
			return nvml.BAR1Memory{
				Bar1Total: 256 * 1024 * 1024, // 256 MB
				Bar1Free:  256 * 1024 * 1024,
				Bar1Used:  0,
			}, nvml.SUCCESS
		}

		// Add process info functions (return empty lists)
		dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
			return []nvml.ProcessInfo{}, nvml.SUCCESS
		}

		dev.GetGraphicsRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
			return []nvml.ProcessInfo{}, nvml.SUCCESS
		}
	}

	return &Engine{
		server:  server,
		config:  config,
		handles: NewHandleTable(),
	}
}

// Init initializes the NVML library.
// Implements reference counting like real NVML.
func (e *Engine) Init() nvml.Return {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		ret := e.server.Init()
		if ret != nvml.SUCCESS {
			return ret
		}
		e.initialized = true
	}
	e.initCount++
	return nvml.SUCCESS
}

// Shutdown shuts down the NVML library.
// Implements reference counting like real NVML.
func (e *Engine) Shutdown() nvml.Return {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		return nvml.ERROR_UNINITIALIZED
	}

	e.initCount--
	if e.initCount <= 0 {
		ret := e.server.Shutdown()
		if ret != nvml.SUCCESS {
			return ret
		}
		e.initialized = false
		e.initCount = 0
		e.handles.Clear()
	}
	return nvml.SUCCESS
}

// IsInitialized returns whether NVML has been initialized.
func (e *Engine) IsInitialized() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.initialized
}

// DeviceGetCount returns the number of GPU devices.
func (e *Engine) DeviceGetCount() (int, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	return e.server.DeviceGetCount()
}

// DeviceGetHandleByIndex returns a device handle for the given index.
func (e *Engine) DeviceGetHandleByIndex(index int) (uintptr, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	device, ret := e.server.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	handle := e.handles.Register(device)
	return handle, nvml.SUCCESS
}

// DeviceGetHandleByUUID returns a device handle for the given UUID.
func (e *Engine) DeviceGetHandleByUUID(uuid string) (uintptr, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	device, ret := e.server.DeviceGetHandleByUUID(uuid)
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	handle := e.handles.Register(device)
	return handle, nvml.SUCCESS
}

// DeviceGetHandleByPciBusId returns a device handle for the given PCI bus ID.
func (e *Engine) DeviceGetHandleByPciBusId(pciBusId string) (uintptr, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	device, ret := e.server.DeviceGetHandleByPciBusId(pciBusId)
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	handle := e.handles.Register(device)
	return handle, nvml.SUCCESS
}

// GetDevice returns the device for the given handle.
func (e *Engine) GetDevice(handle uintptr) nvml.Device {
	return e.handles.Lookup(handle)
}

// SystemGetDriverVersion returns the driver version.
func (e *Engine) SystemGetDriverVersion() (string, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return "", nvml.ERROR_UNINITIALIZED
	}

	return e.server.SystemGetDriverVersion()
}

// SystemGetNVMLVersion returns the NVML version.
func (e *Engine) SystemGetNVMLVersion() (string, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return "", nvml.ERROR_UNINITIALIZED
	}

	return e.server.SystemGetNVMLVersion()
}

// SystemGetCudaDriverVersion returns the CUDA driver version.
func (e *Engine) SystemGetCudaDriverVersion() (int, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		return 0, nvml.ERROR_UNINITIALIZED
	}

	return e.server.SystemGetCudaDriverVersion()
}

