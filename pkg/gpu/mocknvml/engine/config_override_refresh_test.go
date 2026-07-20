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
