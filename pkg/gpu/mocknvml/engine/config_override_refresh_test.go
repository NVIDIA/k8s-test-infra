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

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

// newTestDevice builds a ConfigurableDevice backed by a dgxa100 base device
// and points the package config override store at a temp file with a controllable clock.
func newTestDevice(t *testing.T, base *DeviceConfig) (*ConfigurableDevice, string, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	now := time.Unix(0, 0)
	clock := &now
	configOverrides = newConfigOverrideStoreAt(func() string { return path }, func() time.Time { return *clock })
	t.Cleanup(resetConfigOverrideStoreForTesting)

	srv := dgxa100.New()
	bd := srv.Devices[0].(*mockserver.Device)
	dev := NewConfigurableDevice(0, bd, base, "GPU-test", "0000:01:00.0", 0, nil)
	return dev, path, clock
}

func writeConfigOverride(t *testing.T, path, content string, clock *time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Second) // move past TTL
}

func TestRefresh_InjectsLostThenResets(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{})
	if dev.failureInjector() != nil {
		t.Fatal("device should start healthy")
	}
	writeConfigOverride(t, path, "devices:\n  \"0\":\n    failure:\n      mode: lost\n", clock)
	fi := dev.failureInjector()
	if fi == nil || fi.Mode() != FailureModeLost {
		t.Fatalf("expected lost injector, got %+v", fi)
	}
	// Clear config override -> back to healthy.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Second)
	if dev.failureInjector() != nil {
		t.Fatal("device should recover to healthy after config override removed")
	}
}

func TestRefresh_AllAppliesToDevice(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{})
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n    after_calls: 1\n", clock)
	fi := dev.failureInjector()
	if fi == nil || fi.Mode() != FailureModeECCUncorrectable {
		t.Fatalf("expected ecc_uncorrectable from all: %+v", fi)
	}
	_ = nvml.SUCCESS
}

// TestRefresh_SameModeParamChangeReinstallsInjector verifies that a same-mode
// config override edit to a failure parameter (here after_calls) installs a
// fresh injector at runtime rather than silently keeping the old one.
func TestRefresh_SameModeParamChangeReinstallsInjector(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{})
	writeConfigOverride(t, path, "devices:\n  \"0\":\n    failure:\n      mode: ecc_uncorrectable\n      after_calls: 100\n", clock)
	fi1 := dev.failureInjector()
	require.NotNil(t, fi1, "expected ecc injector")
	fi1.Tick()
	fi1.Tick()
	require.Equal(t, int64(2), fi1.CallCount())

	// Same mode, different after_calls: a fresh injector must be installed so
	// the new parameter takes effect (accumulated call count resets).
	writeConfigOverride(t, path, "devices:\n  \"0\":\n    failure:\n      mode: ecc_uncorrectable\n      after_calls: 1\n", clock)
	fi2 := dev.failureInjector()
	require.NotNil(t, fi2)
	require.NotSame(t, fi1, fi2, "expected a fresh injector after a same-mode param change")
	require.Equal(t, int64(0), fi2.CallCount(), "fresh injector should start with a zero call count")
}

// TestRefresh_HotReloadsDynamicTemperature verifies that editing
// dynamic_metrics.temperature through the config override rebuilds the (otherwise
// construction-time-frozen) simulator so a running consumer observes the new
// value — the mechanism behind `nvml-mock-ctl set dynamic_metrics.temperature`.
func TestRefresh_HotReloadsDynamicTemperature(t *testing.T) {
	// ramp_c/variance_c default to 0 so the simulator returns base_c verbatim.
	base := &DeviceConfig{
		Thermal: &ThermalConfig{TemperatureGPU_C: 30, ShutdownThreshold_C: 100},
		DynamicMetrics: &DynamicMetricsConfig{
			Temperature: &DynamicTemperatureConfig{BaseC: 50},
		},
	}
	dev, path, clock := newTestDevice(t, base)

	if got, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS || got != 50 {
		t.Fatalf("baseline temperature = %d (ret=%v), want 50", got, ret)
	}

	writeConfigOverride(t, path, "devices:\n  \"0\":\n    dynamic_metrics:\n      temperature:\n        base_c: 85\n", clock)
	if got, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS || got != 85 {
		t.Fatalf("after config override temperature = %d (ret=%v), want 85", got, ret)
	}

	// Clearing the config override reverts to the base config's simulator.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Second)
	if got, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS || got != 50 {
		t.Fatalf("after reset temperature = %d (ret=%v), want 50", got, ret)
	}
}

// Memory literals taken from the shipped a100 profile
// (deployments/nvml-mock/helm/nvml-mock/profiles/a100.yaml) so the fixture
// matches what a real deployment reports.
const (
	a100TotalBytes    uint64 = 42949672960 // 40 GiB HBM2e
	a100ReservedBytes uint64 = 611319808   // ~583 MiB
	a100FreeBytes     uint64 = 42338353152 // total - reserved at idle
	// What `nvml-mock-ctl set --gpu 0 memory.used_bytes=1073741824
	// memory.free_bytes=41264611328` writes: 1 GiB in use, free reduced to match.
	overrideUsedBytes uint64 = 1073741824
	overrideFreeBytes uint64 = 41264611328
)

func a100MemoryBase() *DeviceConfig {
	return &DeviceConfig{Memory: &MemoryConfig{
		TotalBytes:    a100TotalBytes,
		ReservedBytes: a100ReservedBytes,
		FreeBytes:     a100FreeBytes,
		UsedBytes:     0,
	}}
}

const usedBytesOverrideDoc = "devices:\n  \"0\":\n    memory:\n      used_bytes: 1073741824\n      free_bytes: 41264611328\n"

// TestRefresh_HotReloadsMemoryUsedBytes verifies that a runtime config override
// of memory.used_bytes is observed by GetMemoryInfo without restarting the
// process — the mechanism behind `nvml-mock-ctl set --gpu 0 memory.used_bytes`.
// Regression guard for #506: the getter used to return a struct baked once at
// construction, so the override silently did nothing until a pod restart.
func TestRefresh_HotReloadsMemoryUsedBytes(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MemoryBase())

	mem, ret := dev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret, "baseline GetMemoryInfo")
	require.Equal(t, uint64(0), mem.Used, "baseline used bytes")
	require.Equal(t, a100TotalBytes, mem.Total, "baseline total bytes")
	require.Equal(t, a100FreeBytes, mem.Free, "baseline free bytes")

	writeConfigOverride(t, path, usedBytesOverrideDoc, clock)

	mem, ret = dev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetMemoryInfo after override")
	require.Equal(t, overrideUsedBytes, mem.Used, "used bytes must follow the runtime override")
	require.Equal(t, overrideFreeBytes, mem.Free, "free bytes must follow the runtime override")
	require.Equal(t, a100TotalBytes, mem.Total, "total bytes must survive a partial memory override")

	// Clearing the override reverts to the profile values; a getter that cached
	// the first effective config would keep reporting 1 GiB used.
	require.NoError(t, os.Remove(path))
	*clock = clock.Add(2 * time.Second)

	mem, ret = dev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetMemoryInfo after override removed")
	require.Equal(t, uint64(0), mem.Used, "used bytes must revert when the override is removed")
	require.Equal(t, a100FreeBytes, mem.Free, "free bytes must revert when the override is removed")
	require.Equal(t, a100TotalBytes, mem.Total, "total bytes must revert when the override is removed")
}

// TestRefresh_HotReloadsMemoryUsedBytes_v2 is the GetMemoryInfo_v2 counterpart.
// v2 already sourced Reserved from the effective config but took Total/Free/Used
// from the same construction-time struct as v1, so it carried the same defect on
// those three fields; Reserved is asserted here to prove the pre-existing
// effective-config read still works.
func TestRefresh_HotReloadsMemoryUsedBytes_v2(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MemoryBase())

	mem, ret := dev.GetMemoryInfo_v2()
	require.Equal(t, nvml.SUCCESS, ret, "baseline GetMemoryInfo_v2")
	require.Equal(t, uint64(0), mem.Used, "baseline used bytes")
	require.Equal(t, a100ReservedBytes, mem.Reserved, "baseline reserved bytes")

	writeConfigOverride(t, path, usedBytesOverrideDoc, clock)

	mem, ret = dev.GetMemoryInfo_v2()
	require.Equal(t, nvml.SUCCESS, ret, "GetMemoryInfo_v2 after override")
	require.Equal(t, overrideUsedBytes, mem.Used, "used bytes must follow the runtime override")
	require.Equal(t, overrideFreeBytes, mem.Free, "free bytes must follow the runtime override")
	require.Equal(t, a100TotalBytes, mem.Total, "total bytes must survive a partial memory override")
	require.Equal(t, a100ReservedBytes, mem.Reserved, "reserved bytes must survive a partial memory override")
}

// TestRefresh_MemoryWithoutConfigKeepsBaseDeviceValues pins the legacy path: a
// device built without a memory block in its YAML must keep reporting the base
// mock device's memory rather than a zeroed struct, even while an unrelated
// override is live.
func TestRefresh_MemoryWithoutConfigKeepsBaseDeviceValues(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{})

	baseline, ret := dev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret, "baseline GetMemoryInfo")
	require.NotZero(t, baseline.Total, "base mock device should report a non-zero total")

	writeConfigOverride(t, path, "devices:\n  \"0\":\n    thermal:\n      temperature_gpu_c: 77\n", clock)

	mem, ret := dev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret, "GetMemoryInfo after unrelated override")
	require.Equal(t, baseline, mem, "memory must be unchanged when the config carries no memory block")
}
