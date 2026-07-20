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

package mockctl

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func TestParseSet_TypesAndNesting(t *testing.T) {
	m, err := ParseSet([]string{"ecc.mode_current=disabled", "failure.after_calls=1", "failure.mode=lost"})
	if err != nil {
		t.Fatal(err)
	}
	ecc := m["ecc"].(map[string]any)
	if ecc["mode_current"] != "disabled" {
		t.Fatalf("bad ecc: %v", ecc)
	}
	fail := m["failure"].(map[string]any)
	if fail["after_calls"] != float64(1) && fail["after_calls"] != 1 {
		t.Fatalf("after_calls should parse numeric: %#v", fail["after_calls"])
	}
}

func TestDocFail_SetsFailureForIndex(t *testing.T) {
	d := &Doc{}
	if err := d.Fail(Target{Index: 2}, "ecc_uncorrectable", 1, 79); err != nil {
		t.Fatal(err)
	}
	f := d.Devices["2"]["failure"].(map[string]any)
	if f["mode"] != "ecc_uncorrectable" {
		t.Fatalf("mode not set: %v", f)
	}
}

func TestDocFail_RejectsBadMode(t *testing.T) {
	if err := (&Doc{}).Fail(Target{All: true}, "banana", 0, 0); err == nil {
		t.Fatal("expected invalid mode error")
	}
}

func TestReset_All(t *testing.T) {
	d := &Doc{All: map[string]any{"x": 1}, Devices: map[string]map[string]any{"0": {"y": 2}}}
	d.Reset(Target{All: true})
	if d.All != nil || len(d.Devices) != 0 {
		t.Fatalf("reset all should clear everything: %+v", d)
	}
}

func TestValidate_RejectsUnknownField(t *testing.T) {
	if err := Validate(&engine.DeviceConfig{}, map[string]any{"nope": 1}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestTemperaturePatch_MergesStaticAndDynamic(t *testing.T) {
	base := &engine.DeviceConfig{
		Thermal: &engine.ThermalConfig{TemperatureGPU_C: 40, ShutdownThreshold_C: 95},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Temperature: &engine.DynamicTemperatureConfig{BaseC: 55, RampC: 10, VarianceC: 3},
		},
	}
	patch := TemperaturePatch(85)
	if err := Validate(base, patch); err != nil {
		t.Fatalf("validate: %v", err)
	}
	merged, err := engine.MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Thermal.TemperatureGPU_C != 85 {
		t.Fatalf("static temp = %d, want 85", merged.Thermal.TemperatureGPU_C)
	}
	// Shutdown threshold from the base must survive the merge.
	if merged.Thermal.ShutdownThreshold_C != 95 {
		t.Fatalf("shutdown threshold = %d, want 95 (base preserved)", merged.Thermal.ShutdownThreshold_C)
	}
	dt := merged.DynamicMetrics.Temperature
	if dt.BaseC != 85 || dt.RampC != 0 || dt.VarianceC != 0 {
		t.Fatalf("dynamic temp = %+v, want base_c=85 ramp_c=0 variance_c=0", dt)
	}
}

func TestPowerPatch_MergesStaticAndDynamic(t *testing.T) {
	base := &engine.DeviceConfig{
		Power: &engine.PowerConfig{CurrentDrawMW: 100000, MinLimitMW: 50000, MaxLimitMW: 700000},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Power: &engine.DynamicPowerConfig{BaseMW: 120000, VarianceMW: 20000},
		},
	}
	patch := PowerPatch(350000)
	if err := Validate(base, patch); err != nil {
		t.Fatalf("validate: %v", err)
	}
	merged, err := engine.MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Power.CurrentDrawMW != 350000 {
		t.Fatalf("static draw = %d, want 350000", merged.Power.CurrentDrawMW)
	}
	if merged.Power.MaxLimitMW != 700000 {
		t.Fatalf("max limit = %d, want 700000 (base preserved)", merged.Power.MaxLimitMW)
	}
	dp := merged.DynamicMetrics.Power
	if dp.BaseMW != 350000 || dp.VarianceMW != 0 {
		t.Fatalf("dynamic power = %+v, want base_mw=350000 variance_mw=0", dp)
	}
}

func TestFanPatch_ForcesCountAndStringSpeed(t *testing.T) {
	// Liquid-cooled base (count 0) must be forced to a visible fan.
	patch := FanPatch(60, 0)
	fan := patch["fan"].(map[string]any)
	if fan["count"] != 1 {
		t.Fatalf("count = %v, want 1 (forced from 0)", fan["count"])
	}
	if fan["speed_percent"] != "60" {
		t.Fatalf("speed_percent = %#v, want string \"60\"", fan["speed_percent"])
	}

	// A larger baseline fan count is preserved.
	if got := FanPatch(30, 3)["fan"].(map[string]any)["count"]; got != 3 {
		t.Fatalf("count = %v, want 3 (baseline preserved)", got)
	}

	base := &engine.DeviceConfig{Fan: &engine.FanConfig{Count: 0, SpeedPercent: "N/A"}}
	if err := Validate(base, patch); err != nil {
		t.Fatalf("validate: %v", err)
	}
	merged, err := engine.MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Fan.Count != 1 || merged.Fan.SpeedPercent != "60" {
		t.Fatalf("merged fan = %+v, want count=1 speed_percent=60", merged.Fan)
	}
}

func TestUtilizationPatch_PinsStaticAndDisablesDynamic(t *testing.T) {
	base := &engine.DeviceConfig{
		Utilization: &engine.UtilizationConfig{GPU: 10, Memory: 5},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Utilization: &engine.DynamicUtilizationConfig{Pattern: "busy", GPUMax: 90},
		},
	}
	patch := UtilizationPatch(90)
	if err := Validate(base, patch); err != nil {
		t.Fatalf("validate: %v", err)
	}
	merged, err := engine.MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Utilization.GPU != 90 || merged.Utilization.Memory != 90 {
		t.Fatalf("static util = %+v, want gpu=90 memory=90", merged.Utilization)
	}
	if merged.DynamicMetrics.Utilization != nil {
		t.Fatalf("dynamic utilization should be disabled (nil), got %+v", merged.DynamicMetrics.Utilization)
	}
}

func TestUtilizationPatch_ZeroIsDeterministic(t *testing.T) {
	// 0% must not fall through the simulator's min==max==0 "unbounded" rule.
	base := &engine.DeviceConfig{
		Utilization: &engine.UtilizationConfig{GPU: 50},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Utilization: &engine.DynamicUtilizationConfig{Pattern: "steady", GPUMin: 20, GPUMax: 80},
		},
	}
	merged, err := engine.MergeDeviceConfig(base, UtilizationPatch(0))
	if err != nil {
		t.Fatal(err)
	}
	if merged.Utilization.GPU != 0 || merged.DynamicMetrics.Utilization != nil {
		t.Fatalf("util 0 = %+v (dyn util %+v), want static gpu=0 and nil dynamic util",
			merged.Utilization, merged.DynamicMetrics.Utilization)
	}
}

func TestClocksPatch_PinsSMAndGraphics(t *testing.T) {
	base := &engine.DeviceConfig{
		Clocks: &engine.ClocksConfig{GraphicsCurrent: 300, SMCurrent: 300, MemoryCurrent: 1200},
	}
	patch := ClocksPatch(1980)
	if err := Validate(base, patch); err != nil {
		t.Fatalf("validate: %v", err)
	}
	merged, err := engine.MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Clocks.GraphicsCurrent != 1980 || merged.Clocks.SMCurrent != 1980 {
		t.Fatalf("clocks = %+v, want graphics/sm = 1980", merged.Clocks)
	}
	if merged.Clocks.MemoryCurrent != 1200 {
		t.Fatalf("memory clock = %d, want 1200 (base preserved)", merged.Clocks.MemoryCurrent)
	}
}

func TestPStatePatch_FormatsPState(t *testing.T) {
	patch := PStatePatch(8)
	if patch["performance_state"] != "P8" {
		t.Fatalf("performance_state = %#v, want \"P8\"", patch["performance_state"])
	}
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, PStatePatch(12))
	if err != nil {
		t.Fatal(err)
	}
	if merged.PerformanceState != "P12" {
		t.Fatalf("performance_state = %q, want P12", merged.PerformanceState)
	}
}

func TestThrottlePatch_AuthoritativeFlags(t *testing.T) {
	patch, err := ThrottlePatch([]string{"thermal"})
	if err != nil {
		t.Fatal(err)
	}
	base := &engine.DeviceConfig{
		ClocksThrottleReasons: &engine.ClocksThrottleReasonsConfig{SWPowerCap: true},
	}
	if err := Validate(base, patch); err != nil {
		t.Fatalf("validate: %v", err)
	}
	merged, err := engine.MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	ctr := merged.ClocksThrottleReasons
	if !ctr.HWThermalSlowdown {
		t.Fatalf("hw_thermal_slowdown should be true: %+v", ctr)
	}
	// Authoritative: a previously-set reason must be cleared.
	if ctr.SWPowerCap {
		t.Fatalf("sw_power_cap should be cleared by authoritative patch: %+v", ctr)
	}
}

func TestThrottlePatch_NoneClearsAll(t *testing.T) {
	patch, err := ThrottlePatch([]string{"none"})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := engine.MergeDeviceConfig(
		&engine.DeviceConfig{ClocksThrottleReasons: &engine.ClocksThrottleReasonsConfig{HWSlowdown: true}},
		patch,
	)
	if err != nil {
		t.Fatal(err)
	}
	if merged.ClocksThrottleReasons.HWSlowdown {
		t.Fatalf("none should clear all reasons: %+v", merged.ClocksThrottleReasons)
	}
}

func TestThrottlePatch_Errors(t *testing.T) {
	if _, err := ThrottlePatch(nil); err == nil {
		t.Fatal("expected error for no reasons")
	}
	if _, err := ThrottlePatch([]string{"banana"}); err == nil {
		t.Fatal("expected error for unknown reason")
	}
	if _, err := ThrottlePatch([]string{"none", "thermal"}); err == nil {
		t.Fatal("expected error combining none with other reasons")
	}
}

func TestResolveTarget_UUID(t *testing.T) {
	cfg := &engine.Config{YAMLConfig: &engine.YAMLConfig{
		Devices: []engine.DeviceOverride{{Index: 3, UUID: "GPU-abc"}},
	}}
	tg, err := ResolveTarget("GPU-abc", cfg)
	if err != nil || tg.Index != 3 {
		t.Fatalf("uuid resolve failed: %+v %v", tg, err)
	}
}
