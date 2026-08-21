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

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func TestParseSet_TypesAndNesting(t *testing.T) {
	m, err := ParseSet([]string{"ecc.mode_current=disabled", "failure.after_calls=1", "failure.mode=lost"})
	require.NoError(t, err)
	ecc := m["ecc"].(map[string]any)
	require.Equal(t, "disabled", ecc["mode_current"])
	fail := m["failure"].(map[string]any)
	require.Contains(t, []any{float64(1), 1}, fail["after_calls"], "after_calls should parse numeric")
}

func TestDocFail_SetsFailureForIndex(t *testing.T) {
	d := &Doc{}
	require.NoError(t, d.Fail(Target{Index: 2}, "ecc_uncorrectable", 1, 79))
	f := d.Devices["2"]["failure"].(map[string]any)
	require.Equal(t, "ecc_uncorrectable", f["mode"])
}

func TestDocFail_RejectsBadMode(t *testing.T) {
	require.Error(t, (&Doc{}).Fail(Target{All: true}, "banana", 0, 0), "expected invalid mode error")
}

func TestDocFail_IsAuthoritative(t *testing.T) {
	d := &Doc{}
	require.NoError(t, d.Fail(Target{Index: 0}, "ecc_uncorrectable", 1, 79))
	// Re-failing with a different mode and no xid must replace the whole
	// failure block, not deep-merge, so the stale xid/after_calls are gone.
	require.NoError(t, d.Fail(Target{Index: 0}, "lost", 0, 0))
	f := d.Devices["0"]["failure"].(map[string]any)
	require.Equal(t, "lost", f["mode"])
	require.NotContains(t, f, "xid", "stale xid should be cleared")
	require.NotContains(t, f, "after_calls", "stale after_calls should be cleared")
}

func TestReset_All(t *testing.T) {
	d := &Doc{All: map[string]any{"x": 1}, Devices: map[string]map[string]any{"0": {"y": 2}}}
	d.Reset(Target{All: true})
	require.Nil(t, d.All)
	require.Empty(t, d.Devices)
}

func TestValidate_RejectsUnknownField(t *testing.T) {
	require.Error(t, Validate(&engine.DeviceConfig{}, map[string]any{"nope": 1}), "expected validation error")
}

func TestTemperaturePatch_MergesStaticAndDynamic(t *testing.T) {
	base := &engine.DeviceConfig{
		Thermal: &engine.ThermalConfig{TemperatureGPU_C: 40, ShutdownThreshold_C: 95},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Temperature: &engine.DynamicTemperatureConfig{BaseC: 55, RampC: 10, VarianceC: 3},
		},
	}
	patch := TemperaturePatch(85)
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Equal(t, 85, merged.Thermal.TemperatureGPU_C)
	// Shutdown threshold from the base must survive the merge.
	require.Equal(t, 95, merged.Thermal.ShutdownThreshold_C, "base shutdown threshold preserved")
	dt := merged.DynamicMetrics.Temperature
	require.Equal(t, 85, dt.BaseC)
	require.Zero(t, dt.RampC)
	require.Zero(t, dt.VarianceC)
}

func TestPowerPatch_MergesStaticAndDynamic(t *testing.T) {
	base := &engine.DeviceConfig{
		Power: &engine.PowerConfig{CurrentDrawMW: 100000, MinLimitMW: 50000, MaxLimitMW: 700000},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Power: &engine.DynamicPowerConfig{BaseMW: 120000, VarianceMW: 20000},
		},
	}
	patch := PowerPatch(350000)
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Equal(t, uint32(350000), merged.Power.CurrentDrawMW)
	require.Equal(t, uint32(700000), merged.Power.MaxLimitMW, "base max limit preserved")
	dp := merged.DynamicMetrics.Power
	require.Equal(t, uint32(350000), dp.BaseMW)
	require.Zero(t, dp.VarianceMW)
}

func TestFanPatch_ForcesCountAndStringSpeed(t *testing.T) {
	// Liquid-cooled base (count 0) must be forced to a visible fan.
	patch := FanPatch(60, 0)
	fan := patch["fan"].(map[string]any)
	require.Equal(t, 1, fan["count"], "count forced from 0")
	require.Equal(t, "60", fan["speed_percent"])

	// A larger baseline fan count is preserved.
	require.Equal(t, 3, FanPatch(30, 3)["fan"].(map[string]any)["count"], "baseline count preserved")

	base := &engine.DeviceConfig{Fan: &engine.FanConfig{Count: 0, SpeedPercent: "N/A"}}
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Equal(t, 1, merged.Fan.Count)
	require.Equal(t, "60", merged.Fan.SpeedPercent)
}

func TestUtilizationPatch_PinsStaticAndDisablesDynamic(t *testing.T) {
	base := &engine.DeviceConfig{
		Utilization: &engine.UtilizationConfig{GPU: 10, Memory: 5},
		DynamicMetrics: &engine.DynamicMetricsConfig{
			Utilization: &engine.DynamicUtilizationConfig{Pattern: "busy", GPUMax: 90},
		},
	}
	patch := UtilizationPatch(90)
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Equal(t, uint32(90), merged.Utilization.GPU)
	require.Equal(t, uint32(90), merged.Utilization.Memory)
	require.Nil(t, merged.DynamicMetrics.Utilization, "dynamic utilization should be disabled")
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
	require.NoError(t, err)
	require.Zero(t, merged.Utilization.GPU)
	require.Nil(t, merged.DynamicMetrics.Utilization)
}

func TestClocksPatch_PinsSMAndGraphics(t *testing.T) {
	base := &engine.DeviceConfig{
		Clocks: &engine.ClocksConfig{GraphicsCurrent: 300, SMCurrent: 300, MemoryCurrent: 1200},
	}
	patch := ClocksPatch(1980)
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Equal(t, uint32(1980), merged.Clocks.GraphicsCurrent)
	require.Equal(t, uint32(1980), merged.Clocks.SMCurrent)
	require.Equal(t, uint32(1200), merged.Clocks.MemoryCurrent, "base memory clock preserved")
}

func TestPStatePatch_FormatsPState(t *testing.T) {
	patch := PStatePatch(8)
	require.Equal(t, "P8", patch["performance_state"])
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, PStatePatch(12))
	require.NoError(t, err)
	require.Equal(t, "P12", merged.PerformanceState)
}

func TestNVLinkErrorPatch_MergesRateAndLinks(t *testing.T) {
	patch := NVLinkErrorPatch(250, []int{0, 3, 7})
	require.NoError(t, Validate(&engine.DeviceConfig{}, patch))
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, patch)
	require.NoError(t, err)
	require.NotNil(t, merged.NVLinkError)
	require.InDelta(t, float64(250), merged.NVLinkError.Rate, 1e-9)
	require.Equal(t, []int{0, 3, 7}, merged.NVLinkError.Links)
}

func TestNVLinkErrorPatch_NoLinksOmitsFilter(t *testing.T) {
	patch := NVLinkErrorPatch(100, nil)
	block, ok := patch["nvlink_error"].(map[string]any)
	require.True(t, ok)
	_, hasLinks := block["links"]
	require.False(t, hasLinks, "empty links must be omitted so injection targets all active links")
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, patch)
	require.NoError(t, err)
	require.Nil(t, merged.NVLinkError.Links)
}

func TestNVLinkErrorPatch_ZeroRateHeals(t *testing.T) {
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, NVLinkErrorPatch(0, nil))
	require.NoError(t, err)
	require.NotNil(t, merged.NVLinkError)
	require.InDelta(t, float64(0), merged.NVLinkError.Rate, 1e-9, "rate 0 is the healthy/no-injection value")
}

func TestSramECCPatch_InjectsCountsAndSource(t *testing.T) {
	patch, err := SramECCPatch(5, "secded", "sm", true)
	require.NoError(t, err)
	require.NoError(t, Validate(&engine.DeviceConfig{}, patch))

	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, patch)
	require.NoError(t, err)
	sram := merged.ECC.SRAM
	require.NotNil(t, sram)
	require.Equal(t, uint64(5), sram.Volatile.UncorrectableSECDED)
	require.Equal(t, uint64(5), sram.Aggregate.UncorrectableSECDED)
	require.Zero(t, sram.Aggregate.UncorrectableParity, "only the requested error type is injected")
	require.Equal(t, uint64(5), sram.UncorrectableSources.SM, "the breakdown must attribute the errors to --source")
	require.Zero(t, sram.UncorrectableSources.L2)
	require.True(t, sram.ThresholdExceeded)
}

// TestSramECCPatch_PreservesEccMode guards the trap in patching a nested block:
// clobbering ecc wholesale would turn ECC off, and an ECC-off GPU reports no
// SRAM counters at all — the injection would silently undo itself.
func TestSramECCPatch_PreservesEccMode(t *testing.T) {
	base := &engine.DeviceConfig{
		ECC: &engine.ECCConfig{ModeCurrent: "enabled", ModePending: "enabled"},
	}
	patch, err := SramECCPatch(3, "parity", "l2", false)
	require.NoError(t, err)

	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Equal(t, "enabled", merged.ECC.ModeCurrent)
	require.Equal(t, uint64(3), merged.ECC.SRAM.Aggregate.UncorrectableParity)
	require.Equal(t, uint64(3), merged.ECC.SRAM.UncorrectableSources.L2)
}

func TestSramECCPatch_ZeroCountHeals(t *testing.T) {
	base := &engine.DeviceConfig{
		ECC: &engine.ECCConfig{
			ModeCurrent: "enabled",
			SRAM: &engine.ECCSramConfig{
				Aggregate:            &engine.ECCSramCountsConfig{UncorrectableSECDED: 9},
				UncorrectableSources: &engine.ECCSramSourcesConfig{SM: 9},
				ThresholdExceeded:    true,
			},
		},
	}
	patch, err := SramECCPatch(0, "secded", "", false)
	require.NoError(t, err)

	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.Zero(t, merged.ECC.SRAM.Aggregate.UncorrectableSECDED, "count 0 is the healthy value")
	require.Zero(t, merged.ECC.SRAM.UncorrectableSources.SM, "healing must clear the source breakdown too")
	require.False(t, merged.ECC.SRAM.ThresholdExceeded, "healing must clear the threshold flag")
}

func TestSramECCPatch_Errors(t *testing.T) {
	_, err := SramECCPatch(1, "banana", "", false)
	require.Error(t, err, "expected error for unknown error type")
	_, err = SramECCPatch(1, "secded", "banana", false)
	require.Error(t, err, "expected error for unknown source")
	_, err = SramECCPatch(1, "correctable", "sm", false)
	require.Error(t, err, "the source breakdown only covers uncorrectable errors")
}

func TestThrottlePatch_AuthoritativeFlags(t *testing.T) {
	patch, err := ThrottlePatch([]string{"thermal"})
	require.NoError(t, err)
	base := &engine.DeviceConfig{
		ClocksThrottleReasons: &engine.ClocksThrottleReasonsConfig{SWPowerCap: true},
	}
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	ctr := merged.ClocksThrottleReasons
	require.True(t, ctr.HWThermalSlowdown, "hw_thermal_slowdown should be true")
	// Authoritative: a previously-set reason must be cleared.
	require.False(t, ctr.SWPowerCap, "sw_power_cap should be cleared by authoritative patch")
}

func TestThrottlePatch_NoneClearsAll(t *testing.T) {
	patch, err := ThrottlePatch([]string{"none"})
	require.NoError(t, err)
	merged, err := engine.MergeDeviceConfig(
		&engine.DeviceConfig{ClocksThrottleReasons: &engine.ClocksThrottleReasonsConfig{HWSlowdown: true}},
		patch,
	)
	require.NoError(t, err)
	require.False(t, merged.ClocksThrottleReasons.HWSlowdown, "none should clear all reasons")
}

func TestThrottlePatch_Errors(t *testing.T) {
	_, err := ThrottlePatch(nil)
	require.Error(t, err, "expected error for no reasons")
	_, err = ThrottlePatch([]string{"banana"})
	require.Error(t, err, "expected error for unknown reason")
	_, err = ThrottlePatch([]string{"none", "thermal"})
	require.Error(t, err, "expected error combining none with other reasons")
}

func TestFabricHealthPatch_InjectsOneCondition(t *testing.T) {
	patch, err := FabricHealthPatch([]string{"route_unhealthy"})
	require.NoError(t, err)
	base := &engine.DeviceConfig{Fabric: &engine.FabricConfig{CliqueID: 4, State: "auto"}}
	require.NoError(t, Validate(base, patch))
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)

	require.NotNil(t, merged.Fabric.Health)
	require.True(t, merged.Fabric.Health.RouteUnhealthy, "route_unhealthy")
	require.False(t, merged.Fabric.Health.RouteRecovery, "neighbouring conditions must stay healthy")
	require.False(t, merged.Fabric.Health.DegradedBandwidth, "neighbouring conditions must stay healthy")
	// Fabric identity is a separate concern and must survive the patch.
	require.Equal(t, uint32(4), merged.Fabric.CliqueID, "clique")
	require.Equal(t, "auto", merged.Fabric.State, "state")
}

// TestFabricHealthPatch_Authoritative pins the same replace-not-accumulate
// contract the throttle command has: a second invocation must describe the
// whole health state, not add to it.
func TestFabricHealthPatch_Authoritative(t *testing.T) {
	patch, err := FabricHealthPatch([]string{"degraded_bandwidth"})
	require.NoError(t, err)
	base := &engine.DeviceConfig{Fabric: &engine.FabricConfig{
		Health: &engine.FabricHealthConfig{RouteUnhealthy: true, IncorrectConfiguration: "no_partition"},
	}}
	merged, err := engine.MergeDeviceConfig(base, patch)
	require.NoError(t, err)
	require.True(t, merged.Fabric.Health.DegradedBandwidth, "degraded_bandwidth")
	require.False(t, merged.Fabric.Health.RouteUnhealthy, "a stale condition must be cleared")
	require.Equal(t, "none", merged.Fabric.Health.IncorrectConfiguration, "a stale misconfiguration must be cleared")
}

func TestFabricHealthPatch_HealthyClears(t *testing.T) {
	patch, err := FabricHealthPatch([]string{"healthy"})
	require.NoError(t, err)
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{Fabric: &engine.FabricConfig{
		Health: &engine.FabricHealthConfig{RouteUnhealthy: true},
	}}, patch)
	require.NoError(t, err)
	require.False(t, merged.Fabric.Health.RouteUnhealthy, "healthy must clear every condition")
}

// TestFabricHealthPatch_ReleasesPinnedSummary covers the interaction with a
// profile that pins fabric.health_summary: the injected conditions would
// otherwise have no effect on the summary nvidia-smi reports, so the patch
// hands the summary back to derivation.
func TestFabricHealthPatch_ReleasesPinnedSummary(t *testing.T) {
	patch, err := FabricHealthPatch([]string{"route_unhealthy"})
	require.NoError(t, err)
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{Fabric: &engine.FabricConfig{
		HealthSummary: "healthy",
	}}, patch)
	require.NoError(t, err)
	require.Equal(t, "auto", merged.Fabric.HealthSummary, "summary must follow the injected conditions")
}

func TestFabricHealthPatch_NamedMisconfiguration(t *testing.T) {
	patch, err := FabricHealthPatch([]string{"no_partition"})
	require.NoError(t, err)
	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, patch)
	require.NoError(t, err)
	require.Equal(t, "no_partition", merged.Fabric.Health.IncorrectConfiguration)
}

func TestFabricHealthPatch_Errors(t *testing.T) {
	_, err := FabricHealthPatch(nil)
	require.Error(t, err, "expected error for no conditions")
	_, err = FabricHealthPatch([]string{"banana"})
	require.Error(t, err, "expected error for unknown condition")
	_, err = FabricHealthPatch([]string{"healthy", "route_unhealthy"})
	require.Error(t, err, "expected error combining healthy with a fault")
}

func TestResolveTarget_UUID(t *testing.T) {
	cfg := &engine.Config{YAMLConfig: &engine.YAMLConfig{
		Devices: []engine.DeviceOverride{{Index: 3, UUID: "GPU-abc"}},
	}}
	tg, err := ResolveTarget("GPU-abc", cfg)
	require.NoError(t, err)
	require.Equal(t, 3, tg.Index)
}

func TestResolveTarget_IndexBounds(t *testing.T) {
	cfg := &engine.Config{NumDevices: 8}

	tg, err := ResolveTarget("7", cfg)
	require.NoError(t, err)
	require.Equal(t, 7, tg.Index)

	_, err = ResolveTarget("8", cfg)
	require.Error(t, err, "index == NumDevices is out of range")
	_, err = ResolveTarget("-1", cfg)
	require.Error(t, err, "negative index is out of range")

	// Without a resolved config (NumDevices unknown) the check is skipped.
	tg, err = ResolveTarget("99", nil)
	require.NoError(t, err)
	require.Equal(t, 99, tg.Index)
}
