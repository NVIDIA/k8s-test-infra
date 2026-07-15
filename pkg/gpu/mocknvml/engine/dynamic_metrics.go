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
	"math/rand/v2"
	"sync"
	"time"
)

// dynamicMetricsSimulator produces time-varying mock metric values when a
// DynamicMetricsConfig is present on a device. All methods are safe for
// concurrent use; when no dynamic sub-config is configured for a given
// metric the simulator returns the caller-supplied static value unchanged.
type dynamicMetricsSimulator struct {
	cfg   *DynamicMetricsConfig
	start time.Time
	// now is overridable in tests so we can deterministically drive
	// ramp_period_sec / burst_period_sec behavior without sleeping.
	now func() time.Time

	mu  sync.Mutex
	rng *rand.Rand
}

// newDynamicMetricsSimulator builds a simulator for the given config.
// A nil cfg is valid and yields a nil simulator, meaning "static mode"
// (all callers are expected to nil-check before invoking).
func newDynamicMetricsSimulator(cfg *DynamicMetricsConfig) *dynamicMetricsSimulator {
	if cfg == nil {
		return nil
	}

	seed1 := uint64(cfg.Seed)
	if seed1 == 0 {
		seed1 = uint64(time.Now().UnixNano())
	}
	// PCG requires two seed words; derive the second deterministically from
	// the first so a zero-valued config stays reproducible within a run.
	seed2 := seed1 ^ 0x9E3779B97F4A7C15

	start := time.Now()
	return &dynamicMetricsSimulator{
		cfg:   cfg,
		start: start,
		now:   time.Now,
		rng:   rand.New(rand.NewPCG(seed1, seed2)),
	}
}

// elapsed returns seconds since the simulator was created.
func (s *dynamicMetricsSimulator) elapsed() float64 {
	return s.now().Sub(s.start).Seconds()
}

// randInt returns a uniformly random integer in [lo, hi]. Callers must hold
// s.mu. If hi <= lo the function returns lo.
func (s *dynamicMetricsSimulator) randInt(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + s.rng.IntN(hi-lo+1)
}

// Temperature returns a dynamic GPU temperature if configured, otherwise
// the supplied static value. The result is clamped to shutdownC when
// shutdownC > 0 so tests can't observe impossible readings.
func (s *dynamicMetricsSimulator) Temperature(static uint32, shutdownC int) uint32 {
	if s == nil || s.cfg == nil || s.cfg.Temperature == nil {
		return static
	}
	tc := s.cfg.Temperature

	value := tc.BaseC
	if tc.RampC > 0 {
		period := tc.RampPeriodSec
		if period <= 0 {
			period = 60
		}
		// Sine shifted to [0, 1] so the ramp adds at most RampC above base.
		phase := 2 * math.Pi * s.elapsed() / float64(period)
		value += int(math.Round(float64(tc.RampC) * 0.5 * (1 + math.Sin(phase))))
	}
	if tc.VarianceC > 0 {
		s.mu.Lock()
		value += s.randInt(-tc.VarianceC, tc.VarianceC)
		s.mu.Unlock()
	}
	if value < 0 {
		value = 0
	}
	if shutdownC > 0 && value > shutdownC {
		value = shutdownC
	}
	return uint32(value)
}

// Power returns a dynamic power reading in milliwatts if configured,
// otherwise the supplied static value. minMW / maxMW (when > 0) clamp the
// result to the device's advertised power limits.
//
// The caller is responsible for supplying a base_mw that sits inside the
// [minMW, maxMW] envelope (the Helm chart seeds per-profile defaults for
// this); a base outside the window will clamp to a bound and stop varying.
func (s *dynamicMetricsSimulator) Power(static, minMW, maxMW uint32) uint32 {
	if s == nil || s.cfg == nil || s.cfg.Power == nil {
		return static
	}
	pc := s.cfg.Power

	value := int64(pc.BaseMW)
	if pc.VarianceMW > 0 {
		s.mu.Lock()
		value += int64(s.randInt(-int(pc.VarianceMW), int(pc.VarianceMW)))
		s.mu.Unlock()
	}
	if value < 0 {
		value = 0
	}
	if minMW > 0 && value < int64(minMW) {
		value = int64(minMW)
	}
	if maxMW > 0 && value > int64(maxMW) {
		value = int64(maxMW)
	}
	return uint32(value)
}

// Utilization returns dynamic GPU/memory utilization percentages if
// configured, otherwise the supplied static values. Returned values are
// always in [0, 100].
func (s *dynamicMetricsSimulator) Utilization(staticGPU, staticMem uint32) (uint32, uint32) {
	if s == nil || s.cfg == nil || s.cfg.Utilization == nil {
		return staticGPU, staticMem
	}
	uc := s.cfg.Utilization

	gpu := s.sampleUtilization(uc, uc.GPUMin, uc.GPUMax)
	mem := s.sampleUtilization(uc, uc.MemoryMin, uc.MemoryMax)
	return gpu, mem
}

// sampleUtilization returns a value in [min, max] according to the
// configured pattern. If max is zero it's treated as 100 so an
// unconfigured "just pattern=busy" config still produces meaningful values.
func (s *dynamicMetricsSimulator) sampleUtilization(uc *DynamicUtilizationConfig, lo, hi uint32) uint32 {
	if hi == 0 && lo == 0 {
		hi = 100
	}
	if hi > 100 {
		hi = 100
	}
	if lo > hi {
		lo = hi
	}

	// Quarter of the range, used to form the "idle" and "busy" sub-bands.
	span := int(hi) - int(lo)
	quarter := span / 4

	pattern := uc.Pattern
	if pattern == "burst" {
		period := uc.BurstPeriodSec
		if period <= 0 {
			period = 30
		}
		phase := int(s.elapsed()/float64(period)) % 2
		if phase == 0 {
			pattern = "idle"
		} else {
			pattern = "busy"
		}
	}

	var bandLo, bandHi int
	switch pattern {
	case "idle":
		bandLo, bandHi = int(lo), int(lo)+quarter
	case "busy":
		bandLo, bandHi = int(hi)-quarter, int(hi)
	default: // "steady" or empty
		bandLo, bandHi = int(lo), int(hi)
	}
	if bandHi < bandLo {
		bandHi = bandLo
	}

	s.mu.Lock()
	v := s.randInt(bandLo, bandHi)
	s.mu.Unlock()
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return uint32(v)
}
