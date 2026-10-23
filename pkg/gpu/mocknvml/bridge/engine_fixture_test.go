// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// bridgeEngine is an initialised engine singleton plus the override path a
// test writes to in order to inject failures, the way `nvml-mock-ctl fail`
// does at runtime.
type bridgeEngine struct {
	*engine.Engine
	overridePath string
}

// newBridgeEngine points the engine singleton at a profile written for one
// test. The profile carries the driver version because a YAML config wins over
// MOCK_NVML_DRIVER_VERSION, and the version is what the exports' own gate
// checks — the whole question for surfaces NVML added recently.
//
// Tests using this cannot run in parallel: the engine is a process-wide
// singleton and the fixture rebuilds it.
func newBridgeEngine(t *testing.T, profile string) *bridgeEngine {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(profile), 0o600))

	overridePath := filepath.Join(dir, "overrides.yaml")
	t.Setenv("MOCK_NVML_CONFIG", configPath)
	t.Setenv("MOCK_NVML_OVERRIDES", overridePath)
	// Drop the override store's read debounce so a write lands on the very
	// next engine call; the default second would otherwise have every
	// injection test sleeping.
	t.Setenv("MOCK_NVML_OVERRIDES_TTL", "1ns")
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)

	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	return &bridgeEngine{Engine: e, overridePath: overridePath}
}

// fail writes an override document that puts every GPU into mode.
func (b *bridgeEngine) fail(t *testing.T, mode string) {
	t.Helper()
	require.NoError(t, b.writeFailure(mode))
}

// writeFailure is fail without a testing handle, for the goroutine that races
// an already-parked wait: require must not be called off the test goroutine.
func (b *bridgeEngine) writeFailure(mode string) error {
	return os.WriteFile(b.overridePath,
		[]byte("version: 1\nall:\n  failure:\n    mode: "+mode+"\n"), 0o600)
}

// heal removes the override document, returning every GPU to healthy.
func (b *bridgeEngine) heal(t *testing.T) {
	t.Helper()
	require.NoError(t, os.Remove(b.overridePath))
}

// handle resolves an NVML device handle for the given index.
func (b *bridgeEngine) handle(t *testing.T, index int) unsafe.Pointer {
	t.Helper()
	h, ret := b.DeviceGetHandleByIndex(index)
	require.Equal(t, nvml.SUCCESS, ret)
	return h
}

// boardID is the PCI-format GPU id the engine reports for an index, which is
// what a system event carries in place of a device handle.
func (b *bridgeEngine) boardID(t *testing.T, index int) uint32 {
	t.Helper()
	id, ret := b.device(t, index).GetBoardId()
	require.Equal(t, nvml.SUCCESS, ret)
	return id
}

// uuid is the GPU UUID the engine reports for an index, the identity the CPER
// cursor filters on.
func (b *bridgeEngine) uuid(t *testing.T, index int) string {
	t.Helper()
	uuid, ret := b.device(t, index).GetUUID()
	require.Equal(t, nvml.SUCCESS, ret)
	return uuid
}

func (b *bridgeEngine) device(t *testing.T, index int) *engine.ConfigurableDevice {
	t.Helper()
	dev := b.LookupConfigurableDevice(b.handle(t, index))
	require.NotNil(t, dev)
	return dev
}

// profile renders a minimal YAML profile: the driver version the version gate
// reads, a device count, and an optional device-defaults body.
func profile(driverVersion string, numDevices int, deviceDefaults string) string {
	out := "version: \"1.0\"\n" +
		"system:\n" +
		"  driver_version: " + driverVersion + "\n" +
		"  nvml_version: 12." + driverVersion + "\n" +
		"  num_devices: " + strconv.Itoa(numDevices) + "\n"
	if deviceDefaults != "" {
		out += "device_defaults:\n" + deviceDefaults
	}
	return out
}

// Driver versions the new exports gate on, named so a test reads as the
// question it is asking.
const (
	// driverWithSystemEvents has the system event set (580.0) but neither the
	// hostname pair (580.95) nor CPER (590.0).
	driverWithSystemEvents = "580.65.06"
	// driverWithHostname is the branch nvmlDeviceGet/SetHostname_v1 appeared in.
	driverWithHostname = "580.95.05"
	// driverWithCPER is the earliest branch plausibly carrying
	// nvmlSystemGetCPER_v1; no released driver exports it yet.
	driverWithCPER = "590.0"
	// driverBeforeSystemEvents predates every one of them.
	driverBeforeSystemEvents = "550.163.01"
)
