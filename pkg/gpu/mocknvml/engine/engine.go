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
	"k8s.io/klog/v2"
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
	engineMu       sync.Mutex // protects engineOnce reset
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
		klog.ErrorS(err, "Failed to create mock server")
		return nvml.ERROR_UNKNOWN
	}

	e.server = server
	e.initCount = 1
	return nvml.SUCCESS
}

// createServer creates and configures the mock server based on configuration
func (e *Engine) createServer() (*MockServer, error) {
	// Create base dgxa100 mock server
	base := dgxa100.New()

	// Apply configuration overrides
	e.applyConfig(base)

	// Wrap in mock server
	return NewMockServer(base), nil
}

func (e *Engine) applyConfig(server *dgxa100.Server) {
	// Override device count if different from default
	if e.config.NumDevices != MaxDevices {
		originalCount := server.DeviceGetCountFunc
		numDevices := e.config.NumDevices
		server.DeviceGetCountFunc = func() (int, nvml.Return) {
			// dgxa100 has MaxDevices devices max, clamp to that
			if numDevices <= MaxDevices {
				return numDevices, nvml.SUCCESS
			}
			return originalCount()
		}
	}

	// Override driver version if specified
	if e.config.DriverVersion != "" {
		driverVersion := e.config.DriverVersion
		server.SystemGetDriverVersionFunc = func() (string, nvml.Return) {
			return driverVersion, nvml.SUCCESS
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

// SystemGetDriverVersion returns the NVIDIA driver version.
func (e *Engine) SystemGetDriverVersion() (string, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return "", nvml.ERROR_UNINITIALIZED
	}

	return e.server.SystemGetDriverVersion()
}

// ResetForTesting resets the singleton engine for test isolation.
// This should only be called in tests.
func ResetForTesting() {
	engineMu.Lock()
	defer engineMu.Unlock()
	engineOnce = sync.Once{}
	engineInstance = nil
}
