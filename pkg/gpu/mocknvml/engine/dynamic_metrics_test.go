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
	"math"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Helpers
// =============================================================================

// staticConfig mirrors a realistic A100 idle snapshot that dynamic-mode tests
// use as their "static baseline" to verify static behavior is preserved
// whenever DynamicMetrics is nil.
func staticConfig() *DeviceConfig {
	return &DeviceConfig{
		Name: "NVIDIA A100-SXM4-40GB",
		Thermal: &ThermalConfig{
			TemperatureGPU_C:    33,
			ShutdownThreshold_C: 92,
		},
		Power: &PowerConfig{
			CurrentDrawMW: 72000,
			MinLimitMW:    100000,
			MaxLimitMW:    400000,
		},
		Utilization: &UtilizationConfig{GPU: 0, Memory: 0},
	}
}

// newSimulator builds a simulator with a fixed start time and a mutable
// virtual clock so tests can drive ramp_period_sec / burst_period_sec
// behavior deterministically.
//
// Returns the simulator and a pointer to the virtual "current time" that
// tests can advance by mutating (*now = now.Add(...)).
func newSimulator(t *testing.T, cfg *DynamicMetricsConfig) (*dynamicMetricsSimulator, *time.Time) {
	t.Helper()
	s := newDynamicMetricsSimulator(cfg)
	require.NotNil(t, s, "newDynamicMetricsSimulator returned nil for non-nil config")
	// Anchor the virtual clock to the simulator's start so elapsed() == 0
	// at the first call, then let tests advance it explicitly.
	now := s.start
	s.now = func() time.Time { return now }
	return s, &now
}

// samplesN collects N samples from f.
func samplesN[T any](n int, f func() T) []T {
	out := make([]T, n)
	for i := 0; i < n; i++ {
		out[i] = f()
	}
	return out
}

// minMaxU32 returns the minimum and maximum values in xs (xs must be non-empty).
func minMaxU32(xs []uint32) (uint32, uint32) {
	lo, hi := xs[0], xs[0]
	for _, v := range xs[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

// meanU32 returns the arithmetic mean of xs.
func meanU32(xs []uint32) float64 {
	var sum float64
	for _, v := range xs {
		sum += float64(v)
	}
	return sum / float64(len(xs))
}

// =============================================================================
// Device-level tests: static fallback, opt-in, concurrency
// =============================================================================

func TestDynamicMetrics_AbsentPreservesStaticBehavior(t *testing.T) {
	dev := newTestDeviceWithConfig(t, staticConfig())

	for i := 0; i < 20; i++ {
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		require.Equal(t, nvml.SUCCESS, ret, "call %d", i)
		require.Equal(t, uint32(33), temp, "call %d", i)

		power, ret := dev.GetPowerUsage()
		require.Equal(t, nvml.SUCCESS, ret, "call %d", i)
		require.Equal(t, uint32(72000), power, "call %d", i)

		util, ret := dev.GetUtilizationRates()
		require.Equal(t, nvml.SUCCESS, ret, "call %d", i)
		require.Zero(t, util.Gpu, "call %d", i)
		require.Zero(t, util.Memory, "call %d", i)
	}
}

func TestDynamicMetrics_SubConfigsAreIndependentlyOptIn(t *testing.T) {
	// Setting only Power must leave temperature and utilization static.
	cfg := staticConfig()
	cfg.DynamicMetrics = &DynamicMetricsConfig{
		Seed:  1,
		Power: &DynamicPowerConfig{BaseMW: 150000, VarianceMW: 1000},
	}
	dev := newTestDeviceWithConfig(t, cfg)

	for i := 0; i < 20; i++ {
		temp, _ := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		require.Equal(t, uint32(33), temp, "temperature must stay static")

		util, _ := dev.GetUtilizationRates()
		require.Zero(t, util.Gpu, "utilization must stay static")
		require.Zero(t, util.Memory, "utilization must stay static")

		// Power is dynamic, so we only assert it's within [149000, 151000].
		power, _ := dev.GetPowerUsage()
		require.True(t, power >= 149000 && power <= 151000, "power %d outside [149000,151000]", power)
	}
}

func TestDynamicMetrics_ConcurrentAccessIsSafe(t *testing.T) {
	cfg := staticConfig()
	cfg.DynamicMetrics = &DynamicMetricsConfig{
		Seed:        3,
		Temperature: &DynamicTemperatureConfig{BaseC: 55, VarianceC: 5},
		Power:       &DynamicPowerConfig{BaseMW: 200000, VarianceMW: 10000},
		Utilization: &DynamicUtilizationConfig{Pattern: "steady", GPUMin: 10, GPUMax: 90},
	}
	dev := newTestDeviceWithConfig(t, cfg)

	// Hammer all three getters concurrently; -race catches any missing
	// synchronization inside the simulator.
	const goroutines, iters = 8, 500
	done := make(chan struct{}, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < iters; j++ {
				_, _ = dev.GetTemperature(nvml.TEMPERATURE_GPU)
				_, _ = dev.GetPowerUsage()
				_, _ = dev.GetUtilizationRates()
			}
		}()
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}
}

// =============================================================================
// Temperature
// =============================================================================

func TestDynamicMetrics_Temperature_ZeroVarianceReturnsBase(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        1,
		Temperature: &DynamicTemperatureConfig{BaseC: 50},
	})
	for i := 0; i < 50; i++ {
		got := s.Temperature(0, 0)
		require.Equal(t, uint32(50), got, "call %d: expected deterministic 50 when variance=0", i)
	}
}

func TestDynamicMetrics_Temperature_VarianceTightBounds(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        12345,
		Temperature: &DynamicTemperatureConfig{BaseC: 50, VarianceC: 5},
	})
	samples := samplesN(2000, func() uint32 { return s.Temperature(0, 0) })
	lo, hi := minMaxU32(samples)
	require.True(t, lo >= 45 && hi <= 55, "samples out of [45,55]: observed [%d,%d]", lo, hi)
	// Uniform noise in [-5,5] should realistically hit both extremes at n=2000.
	require.LessOrEqual(t, lo, uint32(46), "lower tail weak: min=%d, expected <=46", lo)
	require.GreaterOrEqual(t, hi, uint32(54), "upper tail weak: max=%d, expected >=54", hi)
	// Mean should be close to base (50) for uniform noise. Allow 0.5c tolerance.
	got := meanU32(samples)
	require.LessOrEqual(t, math.Abs(got-50.0), 0.5, "mean=%.2f too far from base=50", got)
}

func TestDynamicMetrics_Temperature_RampOscillatesOverPeriod(t *testing.T) {
	// ramp=10, period=100s, no variance. The sine ramp is
	//   offset(t) = round(10 * 0.5 * (1 + sin(2*pi*t/100)))
	// so:
	//   t=0    -> sin=0   -> offset=5
	//   t=25   -> sin=1   -> offset=10
	//   t=50   -> sin=0   -> offset=5
	//   t=75   -> sin=-1  -> offset=0
	//   t=100  -> sin=0   -> offset=5  (period complete)
	s, now := newSimulator(t, &DynamicMetricsConfig{
		Seed:        1,
		Temperature: &DynamicTemperatureConfig{BaseC: 40, RampC: 10, RampPeriodSec: 100},
	})

	cases := []struct {
		offset time.Duration
		want   uint32
	}{
		{0 * time.Second, 45},
		{25 * time.Second, 50},
		{50 * time.Second, 45},
		{75 * time.Second, 40},
		{100 * time.Second, 45},
	}
	for _, tc := range cases {
		*now = s.start.Add(tc.offset)
		got := s.Temperature(0, 0)
		require.Equal(t, tc.want, got, "t=%s", tc.offset)
	}
}

func TestDynamicMetrics_Temperature_ClampedByShutdownThreshold(t *testing.T) {
	// Base+variance deliberately push well past shutdown (80c).
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        42,
		Temperature: &DynamicTemperatureConfig{BaseC: 200, VarianceC: 10},
	})
	for i := 0; i < 200; i++ {
		got := s.Temperature(0, 80)
		require.LessOrEqual(t, got, uint32(80), "call %d: got %d, must be <=80 (shutdown clamp)", i, got)
	}
}

func TestDynamicMetrics_Temperature_NeverNegative(t *testing.T) {
	// Extreme negative swing from variance can't escape below zero.
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        7,
		Temperature: &DynamicTemperatureConfig{BaseC: 1, VarianceC: 50},
	})
	for i := 0; i < 500; i++ {
		got := s.Temperature(0, 0)
		// uint32 can't be negative; assert it's also <=max(base+variance)=51.
		require.LessOrEqual(t, got, uint32(51), "call %d: got %d above base+variance", i, got)
	}
}

func TestDynamicMetrics_Temperature_NilConfigFallsThroughToStatic(t *testing.T) {
	// Temperature config absent but Power present: Temperature() must
	// return the static fallback unchanged.
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  1,
		Power: &DynamicPowerConfig{BaseMW: 100},
	})
	for i := 0; i < 10; i++ {
		got := s.Temperature(77, 0)
		require.Equal(t, uint32(77), got, "expected static 77")
	}
}

// =============================================================================
// Power
// =============================================================================

func TestDynamicMetrics_Power_ZeroVarianceReturnsBase(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  1,
		Power: &DynamicPowerConfig{BaseMW: 250_000},
	})
	for i := 0; i < 50; i++ {
		got := s.Power(0, 0, 0)
		require.Equal(t, uint32(250_000), got, "call %d", i)
	}
}

func TestDynamicMetrics_Power_VarianceTightBounds(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  99,
		Power: &DynamicPowerConfig{BaseMW: 250_000, VarianceMW: 25_000},
	})
	samples := samplesN(2000, func() uint32 { return s.Power(0, 0, 0) })
	lo, hi := minMaxU32(samples)
	require.True(t, lo >= 225_000 && hi <= 275_000, "samples out of [225000, 275000]: observed [%d, %d]", lo, hi)
	// Tails should be visited with n=2000, variance=25000 (~0.01% chance of missing).
	require.LessOrEqual(t, lo, uint32(226_000), "lower tail weak: min=%d", lo)
	require.GreaterOrEqual(t, hi, uint32(274_000), "upper tail weak: max=%d", hi)
	got := meanU32(samples)
	require.LessOrEqual(t, math.Abs(got-250_000), 1000.0, "mean=%.0f too far from base=250000", got)
}

func TestDynamicMetrics_Power_ClampedToConfiguredMinMax(t *testing.T) {
	// Base+variance blows through both bounds; result must stay inside.
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  99,
		Power: &DynamicPowerConfig{BaseMW: 50_000, VarianceMW: 600_000},
	})
	for i := 0; i < 500; i++ {
		got := s.Power(0, 100_000, 400_000)
		require.True(t, got >= 100_000 && got <= 400_000, "call %d: %d outside [100000, 400000]", i, got)
	}
}

func TestDynamicMetrics_Power_ZeroBoundsDisabledClamp(t *testing.T) {
	// min=0, max=0 means "no clamp", so full variance range is reachable.
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  99,
		Power: &DynamicPowerConfig{BaseMW: 100, VarianceMW: 50},
	})
	samples := samplesN(500, func() uint32 { return s.Power(0, 0, 0) })
	lo, hi := minMaxU32(samples)
	require.True(t, lo >= 50 && hi <= 150, "samples %d..%d outside [50,150]", lo, hi)
}

func TestDynamicMetrics_Power_NilConfigFallsThroughToStatic(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        1,
		Temperature: &DynamicTemperatureConfig{BaseC: 40},
	})
	for i := 0; i < 10; i++ {
		got := s.Power(72_000, 0, 0)
		require.Equal(t, uint32(72_000), got, "expected static 72000")
	}
}

// =============================================================================
// Utilization patterns
// =============================================================================

func TestDynamicMetrics_Utilization_Patterns_Bounds(t *testing.T) {
	// Configured range [0, 100] -> quarter=25, so:
	//   idle : [0, 25]
	//   busy : [75, 100]
	//   steady / unknown : [0, 100]
	cases := []struct {
		name     string
		pattern  string
		wantLoLE uint32 // observed min must be <= this
		wantHiGE uint32 // observed max must be >= this
		hardLo   uint32 // observed min must be >= this
		hardHi   uint32 // observed max must be <= this
	}{
		{"idle", "idle", 3, 22, 0, 25},
		{"busy", "busy", 78, 97, 75, 100},
		{"steady", "steady", 10, 90, 0, 100},
		{"unknown_falls_through_to_steady", "totally-unknown", 10, 90, 0, 100},
		{"empty_falls_through_to_steady", "", 10, 90, 0, 100},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newSimulator(t, &DynamicMetricsConfig{
				Seed: 1,
				Utilization: &DynamicUtilizationConfig{
					Pattern: tc.pattern, GPUMin: 0, GPUMax: 100,
					MemoryMin: 0, MemoryMax: 100,
				},
			})
			gpuSamples := make([]uint32, 1000)
			memSamples := make([]uint32, 1000)
			for i := range gpuSamples {
				g, m := s.Utilization(0, 0)
				gpuSamples[i], memSamples[i] = g, m
			}
			for _, xs := range [][]uint32{gpuSamples, memSamples} {
				lo, hi := minMaxU32(xs)
				require.True(t, lo >= tc.hardLo && hi <= tc.hardHi, "out of range [%d,%d]: observed [%d,%d]",
					tc.hardLo, tc.hardHi, lo, hi)
				require.LessOrEqual(t, lo, tc.wantLoLE, "lower tail weak: min=%d expected <=%d", lo, tc.wantLoLE)
				require.GreaterOrEqual(t, hi, tc.wantHiGE, "upper tail weak: max=%d expected >=%d", hi, tc.wantHiGE)
			}
		})
	}
}

func TestDynamicMetrics_Utilization_BurstAlternatesWithTime(t *testing.T) {
	// burst_period_sec=10: phase 0 = idle, phase 1 = busy, phase 2 = idle, ...
	// With range [0,100] (quarter=25): idle samples in [0,25], busy in [75,100].
	s, now := newSimulator(t, &DynamicMetricsConfig{
		Seed: 7,
		Utilization: &DynamicUtilizationConfig{
			Pattern: "burst", GPUMin: 0, GPUMax: 100,
			MemoryMin: 0, MemoryMax: 100, BurstPeriodSec: 10,
		},
	})

	sampleAt := func(offset time.Duration) uint32 {
		*now = s.start.Add(offset)
		g, _ := s.Utilization(0, 0)
		return g
	}

	// Sample multiple times within each phase to be robust to RNG.
	inIdle := func(offset time.Duration) {
		for i := 0; i < 20; i++ {
			got := sampleAt(offset + time.Duration(i)*time.Second/10)
			require.LessOrEqual(t, got, uint32(25), "burst idle phase at %s iter %d: got %d, want <=25", offset, i, got)
		}
	}
	inBusy := func(offset time.Duration) {
		for i := 0; i < 20; i++ {
			got := sampleAt(offset + time.Duration(i)*time.Second/10)
			require.GreaterOrEqual(t, got, uint32(75), "burst busy phase at %s iter %d: got %d, want >=75", offset, i, got)
		}
	}

	inIdle(1 * time.Second)  // phase 0
	inBusy(11 * time.Second) // phase 1
	inIdle(21 * time.Second) // phase 2
	inBusy(31 * time.Second) // phase 3
}

func TestDynamicMetrics_Utilization_GPUAndMemoryUseIndependentRanges(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed: 5,
		Utilization: &DynamicUtilizationConfig{
			Pattern: "steady",
			GPUMin:  60, GPUMax: 80,
			MemoryMin: 10, MemoryMax: 20,
		},
	})
	for i := 0; i < 500; i++ {
		g, m := s.Utilization(0, 0)
		require.True(t, g >= 60 && g <= 80, "call %d: gpu=%d outside [60,80]", i, g)
		require.True(t, m >= 10 && m <= 20, "call %d: mem=%d outside [10,20]", i, m)
	}
}

func TestDynamicMetrics_Utilization_MinEqualsMaxPinsToSingleValue(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed: 5,
		Utilization: &DynamicUtilizationConfig{
			Pattern: "steady",
			GPUMin:  42, GPUMax: 42,
			MemoryMin: 7, MemoryMax: 7,
		},
	})
	for i := 0; i < 50; i++ {
		g, m := s.Utilization(0, 0)
		require.Equal(t, uint32(42), g, "call %d", i)
		require.Equal(t, uint32(7), m, "call %d", i)
	}
}

func TestDynamicMetrics_Utilization_ClampedTo0_100(t *testing.T) {
	// Intentionally insane config: max > 100 must be clamped to 100.
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed: 1,
		Utilization: &DynamicUtilizationConfig{
			Pattern: "steady",
			GPUMin:  50, GPUMax: 200,
			MemoryMin: 50, MemoryMax: 200,
		},
	})
	for i := 0; i < 200; i++ {
		g, m := s.Utilization(0, 0)
		require.True(t, g <= 100 && m <= 100, "call %d: got (%d,%d) with value >100", i, g, m)
	}
}

func TestDynamicMetrics_Utilization_NilConfigFallsThroughToStatic(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        1,
		Temperature: &DynamicTemperatureConfig{BaseC: 50},
	})
	for i := 0; i < 10; i++ {
		g, m := s.Utilization(11, 22)
		require.Equal(t, uint32(11), g, "expected static gpu 11")
		require.Equal(t, uint32(22), m, "expected static mem 22")
	}
}

// =============================================================================
// RNG determinism
// =============================================================================

func TestDynamicMetrics_SameSeedIsReproducible(t *testing.T) {
	build := func() *dynamicMetricsSimulator {
		s, _ := newSimulator(t, &DynamicMetricsConfig{
			Seed:        424242,
			Temperature: &DynamicTemperatureConfig{BaseC: 50, VarianceC: 5},
			Power:       &DynamicPowerConfig{BaseMW: 1000, VarianceMW: 100},
			Utilization: &DynamicUtilizationConfig{
				Pattern: "steady", GPUMin: 0, GPUMax: 100,
				MemoryMin: 0, MemoryMax: 100,
			},
		})
		return s
	}

	a, b := build(), build()
	for i := 0; i < 100; i++ {
		at := a.Temperature(0, 0)
		bt := b.Temperature(0, 0)
		require.Equal(t, at, bt, "call %d: temperature diverged", i)
		ap := a.Power(0, 0, 0)
		bp := b.Power(0, 0, 0)
		require.Equal(t, ap, bp, "call %d: power diverged", i)
		ag, am := a.Utilization(0, 0)
		bg, bm := b.Utilization(0, 0)
		require.Equal(t, ag, bg, "call %d: utilization gpu diverged", i)
		require.Equal(t, am, bm, "call %d: utilization mem diverged", i)
	}
}

func TestDynamicMetrics_DifferentSeedsProduceDifferentSequences(t *testing.T) {
	build := func(seed int64) *dynamicMetricsSimulator {
		s, _ := newSimulator(t, &DynamicMetricsConfig{
			Seed:        seed,
			Temperature: &DynamicTemperatureConfig{BaseC: 50, VarianceC: 5},
		})
		return s
	}
	a, b := build(1), build(2)
	same := 0
	for i := 0; i < 50; i++ {
		if a.Temperature(0, 0) == b.Temperature(0, 0) {
			same++
		}
	}
	// With uniform noise over 11 values (base 50, variance 5), expected
	// collisions ~= 50/11 ~= 4.5. Failing only if essentially everything
	// collided would mean seeds weren't actually independent.
	require.Less(t, same, 40, "expected divergent sequences, but %d/50 matched", same)
}

// =============================================================================
// Dynamic-only configs: no static thermal:/power: section
// =============================================================================
//
// Real-world failure mode (see #244 follow-up): a user enables dynamic
// metrics but does NOT include a `thermal:` / `power:` block in their YAML.
// `GetTemperature` / `GetPowerUsage` must then succeed using the dynamic
// base value rather than returning ERROR_NOT_SUPPORTED (which surfaces in
// nvidia-smi as `[N/A]`).

func TestDynamicMetrics_DeviceLevel_TemperatureWorksWithoutStaticThermal(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-40GB",
		// No Thermal block on purpose.
		DynamicMetrics: &DynamicMetricsConfig{
			Seed:        1,
			Temperature: &DynamicTemperatureConfig{BaseC: 55, VarianceC: 3},
		},
	})

	for i := 0; i < 50; i++ {
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		require.Equal(t, nvml.SUCCESS, ret, "call %d: expected SUCCESS without static thermal", i)
		require.True(t, temp >= 52 && temp <= 58, "call %d: temp %d outside [52,58]", i, temp)
	}
}

func TestDynamicMetrics_DeviceLevel_PowerWorksWithoutStaticPower(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-40GB",
		// No Power block on purpose.
		DynamicMetrics: &DynamicMetricsConfig{
			Seed:  1,
			Power: &DynamicPowerConfig{BaseMW: 250_000, VarianceMW: 25_000},
		},
	})

	for i := 0; i < 50; i++ {
		power, ret := dev.GetPowerUsage()
		require.Equal(t, nvml.SUCCESS, ret, "call %d: expected SUCCESS without static power", i)
		require.True(t, power >= 225_000 && power <= 275_000, "call %d: power %d outside [225000, 275000]", i, power)
	}
}

func TestDynamicMetrics_DeviceLevel_NotSupportedWhenNeitherSectionPresent(t *testing.T) {
	// Neither static thermal/power nor dynamic temperature/power: must
	// still return ERROR_NOT_SUPPORTED so consumers don't see fake zero
	// readings on a misconfigured profile.
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Name: "NVIDIA A100-SXM4-40GB",
		// Set DynamicMetrics with only utilization to prove temp/power
		// stay NOT_SUPPORTED when their own dynamic sub-config is absent.
		DynamicMetrics: &DynamicMetricsConfig{
			Seed:        1,
			Utilization: &DynamicUtilizationConfig{Pattern: "steady"},
		},
	})
	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "expected ERROR_NOT_SUPPORTED for temperature")
	_, ret = dev.GetPowerUsage()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "expected ERROR_NOT_SUPPORTED for power")
}

// =============================================================================
// nil-simulator safety (device-level)
// =============================================================================

func TestDynamicMetrics_NilSimulatorReturnsStatic(t *testing.T) {
	// Exercises the `s == nil` guards on every public method.
	var s *dynamicMetricsSimulator // nil
	require.Equal(t, uint32(42), s.Temperature(42, 100), "nil sim Temperature")
	require.Equal(t, uint32(123), s.Power(123, 0, 200), "nil sim Power")
	g, m := s.Utilization(10, 20)
	require.Equal(t, uint32(10), g, "nil sim Utilization gpu")
	require.Equal(t, uint32(20), m, "nil sim Utilization mem")
}
