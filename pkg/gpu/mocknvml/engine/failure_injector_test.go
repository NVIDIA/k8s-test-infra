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
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// =============================================================================
// Helpers
// =============================================================================

// healthyConfig is the static A100-ish baseline used by failure-injection
// tests so the "no failure" path returns deterministic values to compare
// against the failure-tripped path.
func healthyConfig() *DeviceConfig {
	return &DeviceConfig{
		Name: "NVIDIA A100-SXM4-40GB",
		Thermal: &ThermalConfig{
			TemperatureGPU_C:    33,
			ShutdownThreshold_C: 92,
		},
		Power: &PowerConfig{
			CurrentDrawMW: 72_000,
			MinLimitMW:    100_000,
			MaxLimitMW:    400_000,
		},
		Utilization: &UtilizationConfig{GPU: 0, Memory: 0},
	}
}

// withFailure returns healthyConfig() with the supplied failure block.
func withFailure(f *FailureInjectionConfig) *DeviceConfig {
	cfg := healthyConfig()
	cfg.Failure = f
	return cfg
}

// =============================================================================
// Injector unit tests (no device wiring)
// =============================================================================

func TestFailureInjector_NilConfigYieldsNilInjector(t *testing.T) {
	if got := newFailureInjector(nil); got != nil {
		t.Fatalf("expected nil injector for nil cfg, got %#v", got)
	}
}

func TestFailureInjector_HealthyModeYieldsNilInjector(t *testing.T) {
	for _, mode := range []string{"", "healthy", "not-a-real-mode"} {
		if got := newFailureInjector(&FailureInjectionConfig{Mode: mode}); got != nil {
			t.Fatalf("mode %q: expected nil injector for healthy/unknown mode, got %#v", mode, got)
		}
	}
}

func TestFailureInjector_ModeWithoutTriggersFailsOnFirstTick(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{Mode: FailureModeLost})
	if f == nil {
		t.Fatal("expected non-nil injector for lost mode")
	}
	if f.Triggered() {
		t.Fatal("injector must not be tripped before any Tick()")
	}
	if !f.Tick() {
		t.Fatal("expected first Tick() to trip a mode-only injector")
	}
	if !f.Triggered() {
		t.Fatal("Triggered() must report true after Tick() trips")
	}
	if got, want := f.ErrorReturn(), nvml.ERROR_GPU_IS_LOST; got != want {
		t.Fatalf("ErrorReturn = %v, want %v", got, want)
	}
}

func TestFailureInjector_AfterCallsIsDeterministic(t *testing.T) {
	const N = 5
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:       FailureModeLost,
		AfterCalls: N,
	})

	for i := 1; i < N; i++ {
		if f.Tick() {
			t.Fatalf("call %d: tripped before AfterCalls=%d", i, N)
		}
	}
	if !f.Tick() {
		t.Fatalf("call %d: expected trip at AfterCalls=%d", N, N)
	}
	// And it stays tripped afterwards.
	for i := 0; i < 3; i++ {
		if !f.Tick() {
			t.Fatalf("post-trip call %d: expected sticky trip", i)
		}
	}
}

func TestFailureInjector_ProbabilityZeroNeverTripsOnItsOwn(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:        FailureModeLost,
		Probability: 0,
		AfterCalls:  1_000_000, // huge so AfterCalls cannot fire
		Seed:        42,
	})
	for i := 0; i < 10_000; i++ {
		if f.Tick() {
			t.Fatalf("call %d: probability=0 must not trip", i)
		}
	}
}

func TestFailureInjector_ProbabilityOneTripsOnFirstTick(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:        FailureModeLost,
		Probability: 1.0,
		AfterCalls:  1_000_000, // ensure trip is from probability, not AfterCalls
		Seed:        42,
	})
	if !f.Tick() {
		t.Fatal("probability=1 must trip immediately")
	}
}

func TestFailureInjector_SeedReproducibility(t *testing.T) {
	mk := func() *failureInjector {
		return newFailureInjector(&FailureInjectionConfig{
			Mode:        FailureModeLost,
			Probability: 0.05,
			AfterCalls:  10_000,
			Seed:        12345,
		})
	}

	tripAt := func(f *failureInjector) int {
		for i := 1; i <= 10_000; i++ {
			if f.Tick() {
				return i
			}
		}
		return -1
	}

	a := tripAt(mk())
	b := tripAt(mk())
	if a != b {
		t.Fatalf("same seed must trip at the same call: a=%d b=%d", a, b)
	}
	if a < 1 || a > 10_000 {
		t.Fatalf("expected trip within 10k calls (Probability=0.05 makes p(no-trip) effectively 0), got %d", a)
	}
}

func TestFailureInjector_DifferentSeedsDiverge(t *testing.T) {
	mk := func(seed int64) *failureInjector {
		return newFailureInjector(&FailureInjectionConfig{
			Mode:        FailureModeLost,
			Probability: 0.01,
			AfterCalls:  10_000,
			Seed:        seed,
		})
	}
	tripAt := func(f *failureInjector) int {
		for i := 1; i <= 10_000; i++ {
			if f.Tick() {
				return i
			}
		}
		return -1
	}

	if tripAt(mk(1)) == tripAt(mk(2)) {
		t.Fatal("different seeds should not trip at the exact same call (with high probability)")
	}
}

func TestFailureInjector_ConcurrentTickSafety(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:        FailureModeLost,
		Probability: 0.001,
		AfterCalls:  1_000_000,
		Seed:        99,
	})

	const goroutines, iters = 16, 2_000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = f.Tick()
			}
		}()
	}
	wg.Wait()

	// Only assertion we make is the call count is correct; -race covers
	// the rest of the synchronization properties.
	if got, want := f.CallCount(), int64(goroutines*iters); got != want {
		t.Fatalf("CallCount = %d, want %d", got, want)
	}
}

func TestFailureInjector_XidRequiresTrip(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:       FailureModeLost,
		AfterCalls: 5,
		Xid:        &XidErrorConfig{Code: 79},
	})
	if got := f.Xid(); got != 0 {
		t.Fatalf("Xid() before trip must be 0, got %d", got)
	}
	for i := 0; i < 5; i++ {
		f.Tick()
	}
	if !f.Triggered() {
		t.Fatal("expected trip after AfterCalls")
	}
	if got, want := f.Xid(), uint64(79); got != want {
		t.Fatalf("Xid() after trip = %d, want %d", got, want)
	}
}

// =============================================================================
// Device-level integration: lost / fallen_off_bus modes
// =============================================================================

func TestFailureInjection_DeviceLevel_LostTripsAfterCalls(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode:       FailureModeLost,
		AfterCalls: 3,
	}))

	for i := 1; i <= 2; i++ {
		if temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS || temp != 33 {
			t.Fatalf("call %d before trip: expected (33, SUCCESS), got (%d, %v)", i, temp, ret)
		}
	}
	// Third call trips and immediately surfaces ERROR_GPU_IS_LOST.
	if _, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("trip call: expected ERROR_GPU_IS_LOST, got %v", ret)
	}
	// All subsequent guarded calls keep failing.
	if _, ret := dev.GetPowerUsage(); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("post-trip GetPowerUsage: expected ERROR_GPU_IS_LOST, got %v", ret)
	}
	if _, ret := dev.GetUtilizationRates(); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("post-trip GetUtilizationRates: expected ERROR_GPU_IS_LOST, got %v", ret)
	}
	if _, ret := dev.GetMemoryInfo(); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("post-trip GetMemoryInfo: expected ERROR_GPU_IS_LOST, got %v", ret)
	}
	if _, ret := dev.GetClockInfo(nvml.CLOCK_SM); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("post-trip GetClockInfo: expected ERROR_GPU_IS_LOST, got %v", ret)
	}
}

func TestFailureInjection_DeviceLevel_FallenOffBusBehavesLikeLost(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode: FailureModeFallenOffBus,
	}))
	// Mode without triggers ⇒ trips on first call.
	if _, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("expected ERROR_GPU_IS_LOST, got %v", ret)
	}
}

func TestFailureInjection_HandleLookupFailsAfterTrip(t *testing.T) {
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "550.163",
				NVMLVersion:   "12.550.163",
				NumDevices:    1,
			},
			DeviceDefaults: *withFailure(&FailureInjectionConfig{
				Mode:       FailureModeLost,
				AfterCalls: 1,
			}),
		},
	}
	e := NewEngine(cfg)
	if ret := e.Init(); ret != nvml.SUCCESS {
		t.Fatalf("engine init failed: %v", ret)
	}
	t.Cleanup(func() { _ = e.Shutdown() })

	// Pre-trip handle lookup must succeed (else newTestDeviceWithConfig
	// itself wouldn't be able to drive the failure). This documents that
	// a fresh boot sees the GPU before guarded API calls trip it.
	handle, ret := e.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		t.Fatalf("pre-trip handle lookup: expected SUCCESS, got %v", ret)
	}
	dev := e.LookupDevice(handle).(*ConfigurableDevice)

	// One guarded call trips the device.
	if _, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("expected first guarded call to trip, got %v", ret)
	}

	// Re-lookups now report the GPU as lost (real NVML returns the same
	// error from handle lookups when the kernel driver has marked the
	// device as gone).
	if _, ret := e.DeviceGetHandleByIndex(0); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("post-trip DeviceGetHandleByIndex: expected ERROR_GPU_IS_LOST, got %v", ret)
	}
	if dev.UUID != "" {
		if _, ret := e.DeviceGetHandleByUUID(dev.UUID); ret != nvml.ERROR_GPU_IS_LOST {
			t.Fatalf("post-trip DeviceGetHandleByUUID: expected ERROR_GPU_IS_LOST, got %v", ret)
		}
	}
}

// =============================================================================
// Device-level integration: ecc_uncorrectable mode
// =============================================================================

func TestFailureInjection_ECC_NonZeroAfterTrip(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode:       FailureModeECCUncorrectable,
		AfterCalls: 1,
	}))

	// Pre-trip: counters report zero.
	count, ret := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC)
	if ret != nvml.SUCCESS {
		t.Fatalf("pre-trip GetTotalEccErrors ret: %v", ret)
	}
	if count != 1 {
		// Reasoning: the call above was the AfterCalls=1 trigger; the
		// counter therefore reports 1 (the running call count) on this
		// very call. Subsequent calls report strictly larger values.
		t.Fatalf("first ECC poll: expected 1 (call count), got %d", count)
	}

	count2, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC)
	if count2 <= count {
		t.Fatalf("expected ECC count to grow with subsequent polls: %d -> %d", count, count2)
	}

	// Corrected counter stays zero — the failure is uncorrectable only.
	if c, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC); c != 0 {
		t.Fatalf("corrected counter must stay zero, got %d", c)
	}

	// Per-location counter agrees on device memory.
	loc, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY)
	if loc == 0 {
		t.Fatalf("device-memory uncorrected counter must be > 0 after trip, got 0")
	}

	// L1 cache (different location) stays at zero.
	l1, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L1_CACHE)
	if l1 != 0 {
		t.Fatalf("L1 cache counter must stay zero (we only inject device-memory errors), got %d", l1)
	}

	// Remapped rows surface the failure.
	_, unc, _, failureOccurred, _ := dev.GetRemappedRows()
	if unc == 0 || !failureOccurred {
		t.Fatalf("expected non-zero uncorrectable rows + failure flag, got unc=%d failure=%v", unc, failureOccurred)
	}
}

func TestFailureInjection_ECC_DoesNotBlockHandleLookup(t *testing.T) {
	cfg := &Config{
		NumDevices:    1,
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "550.163",
				NVMLVersion:   "12.550.163",
				NumDevices:    1,
			},
			DeviceDefaults: *withFailure(&FailureInjectionConfig{
				Mode: FailureModeECCUncorrectable,
			}),
		},
	}
	e := NewEngine(cfg)
	_ = e.Init()
	t.Cleanup(func() { _ = e.Shutdown() })

	// Burn one guarded call so the device trips.
	handle, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(handle).(*ConfigurableDevice)
	_, _ = dev.GetTemperature(nvml.TEMPERATURE_GPU)

	// Handle lookup MUST keep working — ecc_uncorrectable ≠ lost.
	if _, ret := e.DeviceGetHandleByIndex(0); ret != nvml.SUCCESS {
		t.Fatalf("ecc_uncorrectable must not affect handle lookup, got %v", ret)
	}
	// And so must other API calls.
	if _, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS {
		t.Fatalf("ecc_uncorrectable must not block GetTemperature, got %v", ret)
	}
}

// =============================================================================
// Xid reporting
// =============================================================================

func TestFailureInjection_GetViolationStatus_HealthyReportsNoViolation(t *testing.T) {
	dev := newTestDeviceWithConfig(t, healthyConfig())
	vt, ret := dev.GetViolationStatus(nvml.PERF_POLICY_POWER)
	if ret != nvml.SUCCESS {
		t.Fatalf("expected SUCCESS, got %v", ret)
	}
	if vt.ViolationTime != 0 || vt.ReferenceTime != 0 {
		t.Fatalf("healthy device must report empty ViolationTime, got %+v", vt)
	}
}

func TestFailureInjection_GetViolationStatus_SurfacesXidAfterTrip(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode:       FailureModeECCUncorrectable, // doesn't return error from getters
		AfterCalls: 2,
		Xid:        &XidErrorConfig{Code: 64}, // 64 = ECC double-bit
	}))

	// Trip the device with one guarded call.
	if _, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS {
		t.Fatalf("setup call 1: expected SUCCESS, got %v", ret)
	}

	// Pre-trip: violation status should still report zero.
	vt, _ := dev.GetViolationStatus(nvml.PERF_POLICY_POWER)
	// At this point the device has just tripped (AfterCalls=2 met by the
	// SECOND tick, which is GetViolationStatus itself). So we expect Xid
	// to surface.
	if vt.ViolationTime != 64 {
		t.Fatalf("post-trip GetViolationStatus.ViolationTime = %d, want 64 (Xid)", vt.ViolationTime)
	}
	if vt.ReferenceTime == 0 {
		t.Fatalf("expected non-zero ReferenceTime, got 0")
	}
}

func TestFailureInjection_GetViolationStatus_LostModeReturnsError(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode: FailureModeLost,
		Xid:  &XidErrorConfig{Code: 79},
	}))
	// Mode without triggers ⇒ trips on first Tick(). GetViolationStatus
	// is guarded, so the very first call returns the error code (we
	// never get to populate the Xid in the response).
	if _, ret := dev.GetViolationStatus(nvml.PERF_POLICY_POWER); ret != nvml.ERROR_GPU_IS_LOST {
		t.Fatalf("expected ERROR_GPU_IS_LOST, got %v", ret)
	}
}

// =============================================================================
// Override merge
// =============================================================================

func TestFailureInjection_MergeDeviceOverride(t *testing.T) {
	base := DeviceConfig{
		Failure: &FailureInjectionConfig{Mode: FailureModeHealthy},
	}
	override := &DeviceOverride{
		Index: 0,
		DeviceConfig: DeviceConfig{
			Failure: &FailureInjectionConfig{
				Mode:       FailureModeLost,
				AfterCalls: 5,
				Xid:        &XidErrorConfig{Code: 79},
			},
		},
	}

	mergeDeviceOverride(&base, override)

	if base.Failure == nil || base.Failure.Mode != FailureModeLost {
		t.Fatalf("expected override to replace base.Failure with lost mode, got %+v", base.Failure)
	}
	if base.Failure.AfterCalls != 5 {
		t.Fatalf("AfterCalls not propagated: got %d", base.Failure.AfterCalls)
	}
	if base.Failure.Xid == nil || base.Failure.Xid.Code != 79 {
		t.Fatalf("Xid not propagated: %+v", base.Failure.Xid)
	}
}

// =============================================================================
// Healthy default preserves all existing semantics
// =============================================================================

func TestFailureInjection_HealthyConfigIsNoOp(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode: FailureModeHealthy,
	}))
	if dev.failure != nil {
		t.Fatalf("healthy mode must not allocate a failureInjector, got %#v", dev.failure)
	}
	for i := 0; i < 50; i++ {
		if temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret != nvml.SUCCESS || temp != 33 {
			t.Fatalf("call %d: expected (33, SUCCESS), got (%d, %v)", i, temp, ret)
		}
	}
}
