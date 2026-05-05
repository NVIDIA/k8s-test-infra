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
	if s == nil {
		t.Fatal("newDynamicMetricsSimulator returned nil for non-nil config")
	}
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
		if temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS || temp != 33 {
			t.Fatalf("call %d: expected (33, SUCCESS), got (%d, %v)", i, temp, ret)
		}
		if power, ret := dev.GetPowerUsage(); ret != nvml.SUCCESS || power != 72000 {
			t.Fatalf("call %d: expected (72000, SUCCESS), got (%d, %v)", i, power, ret)
		}
		util, ret := dev.GetUtilizationRates()
		if ret != nvml.SUCCESS || util.Gpu != 0 || util.Memory != 0 {
			t.Fatalf("call %d: expected (0,0,SUCCESS), got (%d,%d,%v)", i, util.Gpu, util.Memory, ret)
		}
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
		if temp, _ := dev.GetTemperature(nvml.TEMPERATURE_GPU); temp != 33 {
			t.Fatalf("temperature must stay static, got %d", temp)
		}
		if util, _ := dev.GetUtilizationRates(); util.Gpu != 0 || util.Memory != 0 {
			t.Fatalf("utilization must stay static, got %d/%d", util.Gpu, util.Memory)
		}
		// Power is dynamic, so we only assert it's within [149000, 151000].
		power, _ := dev.GetPowerUsage()
		if power < 149000 || power > 151000 {
			t.Fatalf("power %d outside [149000,151000]", power)
		}
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
		if got := s.Temperature(0, 0); got != 50 {
			t.Fatalf("call %d: expected deterministic 50 when variance=0, got %d", i, got)
		}
	}
}

func TestDynamicMetrics_Temperature_VarianceTightBounds(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        12345,
		Temperature: &DynamicTemperatureConfig{BaseC: 50, VarianceC: 5},
	})
	samples := samplesN(2000, func() uint32 { return s.Temperature(0, 0) })
	lo, hi := minMaxU32(samples)
	if lo < 45 || hi > 55 {
		t.Fatalf("samples out of [45,55]: observed [%d,%d]", lo, hi)
	}
	// Uniform noise in [-5,5] should realistically hit both extremes at n=2000.
	if lo > 46 {
		t.Errorf("lower tail weak: min=%d, expected <=46", lo)
	}
	if hi < 54 {
		t.Errorf("upper tail weak: max=%d, expected >=54", hi)
	}
	// Mean should be close to base (50) for uniform noise. Allow 0.5c tolerance.
	if got := meanU32(samples); math.Abs(got-50.0) > 0.5 {
		t.Errorf("mean=%.2f too far from base=50", got)
	}
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
		if got := s.Temperature(0, 0); got != tc.want {
			t.Errorf("t=%s: got %d, want %d", tc.offset, got, tc.want)
		}
	}
}

func TestDynamicMetrics_Temperature_ClampedByShutdownThreshold(t *testing.T) {
	// Base+variance deliberately push well past shutdown (80c).
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        42,
		Temperature: &DynamicTemperatureConfig{BaseC: 200, VarianceC: 10},
	})
	for i := 0; i < 200; i++ {
		if got := s.Temperature(0, 80); got > 80 {
			t.Fatalf("call %d: got %d, must be <=80 (shutdown clamp)", i, got)
		}
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
		if got > 51 {
			t.Fatalf("call %d: got %d above base+variance", i, got)
		}
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
		if got := s.Temperature(77, 0); got != 77 {
			t.Fatalf("expected static 77, got %d", got)
		}
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
		if got := s.Power(0, 0, 0); got != 250_000 {
			t.Fatalf("call %d: expected 250000, got %d", i, got)
		}
	}
}

func TestDynamicMetrics_Power_VarianceTightBounds(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  99,
		Power: &DynamicPowerConfig{BaseMW: 250_000, VarianceMW: 25_000},
	})
	samples := samplesN(2000, func() uint32 { return s.Power(0, 0, 0) })
	lo, hi := minMaxU32(samples)
	if lo < 225_000 || hi > 275_000 {
		t.Fatalf("samples out of [225000, 275000]: observed [%d, %d]", lo, hi)
	}
	// Tails should be visited with n=2000, variance=25000 (~0.01% chance of missing).
	if lo > 226_000 {
		t.Errorf("lower tail weak: min=%d", lo)
	}
	if hi < 274_000 {
		t.Errorf("upper tail weak: max=%d", hi)
	}
	if got := meanU32(samples); math.Abs(got-250_000) > 1_000 {
		t.Errorf("mean=%.0f too far from base=250000", got)
	}
}

func TestDynamicMetrics_Power_ClampedToConfiguredMinMax(t *testing.T) {
	// Base+variance blows through both bounds; result must stay inside.
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:  99,
		Power: &DynamicPowerConfig{BaseMW: 50_000, VarianceMW: 600_000},
	})
	for i := 0; i < 500; i++ {
		got := s.Power(0, 100_000, 400_000)
		if got < 100_000 || got > 400_000 {
			t.Fatalf("call %d: %d outside [100000, 400000]", i, got)
		}
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
	if lo < 50 || hi > 150 {
		t.Fatalf("samples %d..%d outside [50,150]", lo, hi)
	}
}

func TestDynamicMetrics_Power_NilConfigFallsThroughToStatic(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        1,
		Temperature: &DynamicTemperatureConfig{BaseC: 40},
	})
	for i := 0; i < 10; i++ {
		if got := s.Power(72_000, 0, 0); got != 72_000 {
			t.Fatalf("expected static 72000, got %d", got)
		}
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
		name       string
		pattern    string
		wantLoLE   uint32 // observed min must be <= this
		wantHiGE   uint32 // observed max must be >= this
		hardLo     uint32 // observed min must be >= this
		hardHi     uint32 // observed max must be <= this
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
				if lo < tc.hardLo || hi > tc.hardHi {
					t.Fatalf("out of range [%d,%d]: observed [%d,%d]",
						tc.hardLo, tc.hardHi, lo, hi)
				}
				if lo > tc.wantLoLE {
					t.Errorf("lower tail weak: min=%d expected <=%d", lo, tc.wantLoLE)
				}
				if hi < tc.wantHiGE {
					t.Errorf("upper tail weak: max=%d expected >=%d", hi, tc.wantHiGE)
				}
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
			if got := sampleAt(offset + time.Duration(i)*time.Second/10); got > 25 {
				t.Fatalf("burst idle phase at %s iter %d: got %d, want <=25", offset, i, got)
			}
		}
	}
	inBusy := func(offset time.Duration) {
		for i := 0; i < 20; i++ {
			if got := sampleAt(offset + time.Duration(i)*time.Second/10); got < 75 {
				t.Fatalf("burst busy phase at %s iter %d: got %d, want >=75", offset, i, got)
			}
		}
	}

	inIdle(1 * time.Second)   // phase 0
	inBusy(11 * time.Second)  // phase 1
	inIdle(21 * time.Second)  // phase 2
	inBusy(31 * time.Second)  // phase 3
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
		if g < 60 || g > 80 {
			t.Fatalf("call %d: gpu=%d outside [60,80]", i, g)
		}
		if m < 10 || m > 20 {
			t.Fatalf("call %d: mem=%d outside [10,20]", i, m)
		}
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
		if g != 42 || m != 7 {
			t.Fatalf("call %d: got (%d,%d), want (42,7)", i, g, m)
		}
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
		if g > 100 || m > 100 {
			t.Fatalf("call %d: got (%d,%d) with value >100", i, g, m)
		}
	}
}

func TestDynamicMetrics_Utilization_NilConfigFallsThroughToStatic(t *testing.T) {
	s, _ := newSimulator(t, &DynamicMetricsConfig{
		Seed:        1,
		Temperature: &DynamicTemperatureConfig{BaseC: 50},
	})
	for i := 0; i < 10; i++ {
		g, m := s.Utilization(11, 22)
		if g != 11 || m != 22 {
			t.Fatalf("expected static (11,22), got (%d,%d)", g, m)
		}
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
		if at != bt {
			t.Fatalf("call %d: temperature diverged %d vs %d", i, at, bt)
		}
		ap := a.Power(0, 0, 0)
		bp := b.Power(0, 0, 0)
		if ap != bp {
			t.Fatalf("call %d: power diverged %d vs %d", i, ap, bp)
		}
		ag, am := a.Utilization(0, 0)
		bg, bm := b.Utilization(0, 0)
		if ag != bg || am != bm {
			t.Fatalf("call %d: utilization diverged (%d,%d) vs (%d,%d)", i, ag, am, bg, bm)
		}
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
	if same >= 40 {
		t.Fatalf("expected divergent sequences, but %d/50 matched", same)
	}
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
		if ret != nvml.SUCCESS {
			t.Fatalf("call %d: expected SUCCESS without static thermal, got %v", i, ret)
		}
		if temp < 52 || temp > 58 {
			t.Fatalf("call %d: temp %d outside [52,58]", i, temp)
		}
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
		if ret != nvml.SUCCESS {
			t.Fatalf("call %d: expected SUCCESS without static power, got %v", i, ret)
		}
		if power < 225_000 || power > 275_000 {
			t.Fatalf("call %d: power %d outside [225000, 275000]", i, power)
		}
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
	if _, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.ERROR_NOT_SUPPORTED {
		t.Errorf("expected ERROR_NOT_SUPPORTED for temperature, got %v", ret)
	}
	if _, ret := dev.GetPowerUsage(); ret != nvml.ERROR_NOT_SUPPORTED {
		t.Errorf("expected ERROR_NOT_SUPPORTED for power, got %v", ret)
	}
}

// =============================================================================
// nil-simulator safety (device-level)
// =============================================================================

func TestDynamicMetrics_NilSimulatorReturnsStatic(t *testing.T) {
	// Exercises the `s == nil` guards on every public method.
	var s *dynamicMetricsSimulator // nil
	if got := s.Temperature(42, 100); got != 42 {
		t.Errorf("nil sim Temperature: got %d, want 42", got)
	}
	if got := s.Power(123, 0, 200); got != 123 {
		t.Errorf("nil sim Power: got %d, want 123", got)
	}
	if g, m := s.Utilization(10, 20); g != 10 || m != 20 {
		t.Errorf("nil sim Utilization: got (%d,%d), want (10,20)", g, m)
	}
}
