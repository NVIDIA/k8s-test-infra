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
	"github.com/stretchr/testify/require"
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
	got := newFailureInjector(nil)
	require.Nil(t, got, "expected nil injector for nil cfg")
}

func TestFailureInjector_HealthyModeYieldsNilInjector(t *testing.T) {
	for _, mode := range []string{"", "healthy", "not-a-real-mode"} {
		got := newFailureInjector(&FailureInjectionConfig{Mode: mode})
		require.Nil(t, got, "mode %q: expected nil injector for healthy/unknown mode", mode)
	}
}

func TestFailureInjector_ModeWithoutTriggersFailsOnFirstTick(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{Mode: FailureModeLost})
	require.NotNil(t, f, "expected non-nil injector for lost mode")
	require.False(t, f.Triggered(), "injector must not be tripped before any Tick()")
	require.True(t, f.Tick(), "expected first Tick() to trip a mode-only injector")
	require.True(t, f.Triggered(), "Triggered() must report true after Tick() trips")
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, f.ErrorReturn(), "ErrorReturn")
}

func TestFailureInjector_AfterCallsIsDeterministic(t *testing.T) {
	const N = 5
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:       FailureModeLost,
		AfterCalls: N,
	})

	for i := 1; i < N; i++ {
		require.False(t, f.Tick(), "call %d: tripped before AfterCalls=%d", i, N)
	}
	require.True(t, f.Tick(), "call %d: expected trip at AfterCalls=%d", N, N)
	// And it stays tripped afterwards.
	for i := 0; i < 3; i++ {
		require.True(t, f.Tick(), "post-trip call %d: expected sticky trip", i)
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
		require.False(t, f.Tick(), "call %d: probability=0 must not trip", i)
	}
}

func TestFailureInjector_ProbabilityOneTripsOnFirstTick(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:        FailureModeLost,
		Probability: 1.0,
		AfterCalls:  1_000_000, // ensure trip is from probability, not AfterCalls
		Seed:        42,
	})
	require.True(t, f.Tick(), "probability=1 must trip immediately")
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
	require.Equal(t, a, b, "same seed must trip at the same call: a=%d b=%d", a, b)
	require.True(t, a >= 1 && a <= 10_000, "expected trip within 10k calls (Probability=0.05 makes p(no-trip) effectively 0), got %d", a)
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

	require.NotEqual(t, tripAt(mk(1)), tripAt(mk(2)), "different seeds should not trip at the exact same call (with high probability)")
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
	require.Equal(t, int64(goroutines*iters), f.CallCount(), "CallCount")
}

func TestFailureInjector_XidRequiresTrip(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{
		Mode:       FailureModeLost,
		AfterCalls: 5,
		Xid:        &XidErrorConfig{Code: 79},
	})
	require.Zero(t, f.Xid(), "Xid() before trip must be 0")
	for i := 0; i < 5; i++ {
		f.Tick()
	}
	require.True(t, f.Triggered(), "expected trip after AfterCalls")
	require.Equal(t, uint64(79), f.Xid(), "Xid() after trip")
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
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		require.Equal(t, nvml.SUCCESS, ret, "call %d before trip", i)
		require.Equal(t, uint32(33), temp, "call %d before trip", i)
	}
	// Third call trips and immediately surfaces ERROR_GPU_IS_LOST.
	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "trip call")
	// All subsequent guarded calls keep failing.
	_, ret = dev.GetPowerUsage()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "post-trip GetPowerUsage")
	_, ret = dev.GetUtilizationRates()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "post-trip GetUtilizationRates")
	_, ret = dev.GetMemoryInfo()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "post-trip GetMemoryInfo")
	_, ret = dev.GetClockInfo(nvml.CLOCK_SM)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "post-trip GetClockInfo")
}

func TestFailureInjection_DeviceLevel_FallenOffBusBehavesLikeLost(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode: FailureModeFallenOffBus,
	}))
	// Mode without triggers ⇒ trips on first call.
	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "expected ERROR_GPU_IS_LOST")
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
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "engine init failed")
	t.Cleanup(func() { _ = e.Shutdown() })

	// Pre-trip handle lookup must succeed (else newTestDeviceWithConfig
	// itself wouldn't be able to drive the failure). This documents that
	// a fresh boot sees the GPU before guarded API calls trip it.
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip handle lookup")
	dev := e.LookupDevice(handle).(*ConfigurableDevice)

	// One guarded call trips the device.
	_, ret = dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "expected first guarded call to trip")

	// Re-lookups now report the GPU as lost (real NVML returns the same
	// error from handle lookups when the kernel driver has marked the
	// device as gone).
	_, ret = e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "post-trip DeviceGetHandleByIndex")
	if dev.UUID != "" {
		_, ret = e.DeviceGetHandleByUUID(dev.UUID)
		require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "post-trip DeviceGetHandleByUUID")
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
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetTotalEccErrors ret")
	// Reasoning: the call above was the AfterCalls=1 trigger; the
	// counter therefore reports 1 (the running call count) on this
	// very call. Subsequent calls report strictly larger values.
	require.Equal(t, uint64(1), count, "first ECC poll: expected 1 (call count)")

	count2, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC)
	require.Greater(t, count2, count, "expected ECC count to grow with subsequent polls")

	// Corrected counter stays zero — the failure is uncorrectable only.
	c, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC)
	require.Zero(t, c, "corrected counter must stay zero")

	// Per-location counter agrees on device memory.
	loc, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY)
	require.NotZero(t, loc, "device-memory uncorrected counter must be > 0 after trip")

	// L1 cache (different location) stays at zero.
	l1, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L1_CACHE)
	require.Zero(t, l1, "L1 cache counter must stay zero (we only inject device-memory errors)")

	// Remapped rows surface the failure.
	_, unc, _, failureOccurred, _ := dev.GetRemappedRows()
	require.NotZero(t, unc, "expected non-zero uncorrectable rows")
	require.True(t, failureOccurred, "expected failure flag")
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
	_, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret, "ecc_uncorrectable must not affect handle lookup")
	// And so must other API calls.
	_, ret = dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.SUCCESS, ret, "ecc_uncorrectable must not block GetTemperature")
}

// =============================================================================
// Xid reporting
// =============================================================================

func TestFailureInjection_GetViolationStatus_HealthyReportsNoViolation(t *testing.T) {
	dev := newTestDeviceWithConfig(t, healthyConfig())
	vt, ret := dev.GetViolationStatus(nvml.PERF_POLICY_POWER)
	require.Equal(t, nvml.SUCCESS, ret, "expected SUCCESS")
	require.Zero(t, vt.ViolationTime, "healthy device must report empty ViolationTime")
	require.Zero(t, vt.ReferenceTime, "healthy device must report empty ReferenceTime")
}

func TestFailureInjection_GetViolationStatus_StaysSpecCompliantAfterTrip(t *testing.T) {
	// Regression test for the previous behavior where GetViolationStatus
	// overloaded `ViolationTime.ViolationTime` (officially throttle ns)
	// to carry the configured Xid code. Per NVML semantics that field
	// is reserved for cumulative violation time in nanoseconds, so
	// monitoring stacks that read it per spec misinterpreted the Xid.
	// The Xid is now delivered via the NVML event set
	// (NVML_EVENT_TYPE_XID_CRITICAL_ERROR) — see
	// TestEngine_PendingXidEvent_DeliveredOnceAfterTrip below.
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode:       FailureModeECCUncorrectable, // doesn't return error from getters
		AfterCalls: 2,
		Xid:        &XidErrorConfig{Code: 64}, // 64 = ECC double-bit
	}))

	// Trip the device with one guarded call.
	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.SUCCESS, ret, "setup call 1")

	// At this point the device has just tripped (AfterCalls=2 met by the
	// SECOND tick, which is GetViolationStatus itself). It must NOT
	// stuff the Xid into the violation_time field; both fields stay
	// at their healthy zero values.
	vt, ret := dev.GetViolationStatus(nvml.PERF_POLICY_POWER)
	require.Equal(t, nvml.SUCCESS, ret, "expected SUCCESS")
	require.Zero(t, vt.ViolationTime, "ViolationTime must be 0 ns post-trip (Xid no longer overloaded)")
	require.Zero(t, vt.ReferenceTime, "ReferenceTime must be 0 ns post-trip")
}

func TestFailureInjection_GetViolationStatus_LostModeReturnsError(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode: FailureModeLost,
		Xid:  &XidErrorConfig{Code: 79},
	}))
	// Mode without triggers ⇒ trips on first Tick(). GetViolationStatus
	// is guarded, so the very first call returns the error code (we
	// never get to populate the Xid in the response).
	_, ret := dev.GetViolationStatus(nvml.PERF_POLICY_POWER)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "expected ERROR_GPU_IS_LOST")
}

// =============================================================================
// Engine event-set delivery (NVML_EVENT_TYPE_XID_CRITICAL_ERROR)
// =============================================================================

func TestEngine_PendingXidEvent_NoneWhenHealthy(t *testing.T) {
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
			DeviceDefaults: *healthyConfig(),
		},
	}
	e := NewEngine(cfg)
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "engine init failed")
	t.Cleanup(func() { _ = e.Shutdown() })

	_, _, ok := e.PendingXidEvent()
	require.False(t, ok, "healthy engine must not have pending Xid events")
}

func TestEngine_PendingXidEvent_DeliveredOnceAfterTrip(t *testing.T) {
	// Two devices: one with a Xid configured, one without. Only the
	// first should ever produce an event; once delivered, repeated
	// PendingXidEvent calls return (0, 0, false).
	cfg := &Config{
		NumDevices:    2,
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "550.163",
				NVMLVersion:   "12.550.163",
				NumDevices:    2,
			},
			DeviceDefaults: *withFailure(&FailureInjectionConfig{
				Mode:       FailureModeECCUncorrectable,
				AfterCalls: 1,
				Xid:        &XidErrorConfig{Code: 79},
			}),
		},
	}
	e := NewEngine(cfg)
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "engine init failed")
	t.Cleanup(func() { _ = e.Shutdown() })

	// Before any guarded call trips the device, no event is queued —
	// PendingXidEvent must not synthesize one out of mere config.
	_, _, ok := e.PendingXidEvent()
	require.False(t, ok, "pre-trip: PendingXidEvent must report no event")

	// Trip device 0 with a single guarded call (AfterCalls=1).
	h0, _ := e.DeviceGetHandleByIndex(0)
	dev0 := e.LookupDevice(h0).(*ConfigurableDevice)
	_, ret = dev0.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.SUCCESS, ret, "trip call: expected SUCCESS (ecc_uncorrectable doesn't error)")

	// First wait delivers the Xid event with the right device handle.
	gotHandle, gotXid, ok := e.PendingXidEvent()
	require.True(t, ok, "post-trip: expected pending Xid event, got none")
	require.Equal(t, uint64(79), gotXid, "Xid")
	require.NotZero(t, gotHandle, "expected non-zero device handle")
	require.Equal(t, h0, gotHandle, "expected device handle for index 0")

	// Second wait must NOT redeliver the same Xid (matches real NVML
	// where each critical Xid fires exactly once per occurrence).
	_, _, ok = e.PendingXidEvent()
	require.False(t, ok, "Xid must be delivered at most once; got duplicate event")
}

func TestEngine_PendingXidEvent_NoEventWithoutXidConfig(t *testing.T) {
	// Device trips into ecc_uncorrectable but has no `xid:` block. We
	// surface ECC counters via GetTotalEccErrors; we must NOT fabricate
	// a Xid event from a Xid-less config.
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
				Mode:       FailureModeECCUncorrectable,
				AfterCalls: 1,
				// no Xid configured
			}),
		},
	}
	e := NewEngine(cfg)
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "engine init failed")
	t.Cleanup(func() { _ = e.Shutdown() })

	h, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(h).(*ConfigurableDevice)
	_, ret = dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.SUCCESS, ret, "trip call")

	_, _, ok := e.PendingXidEvent()
	require.False(t, ok, "Xid-less config must produce no event")
}

// =============================================================================
// Identity getters guarded by the lost / fallen_off_bus modes
// =============================================================================

func TestFailureInjection_IdentityGettersFailWhenLost(t *testing.T) {
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
				AfterCalls: 2, // identity getters don't tick; trip via 2 GetTemperature calls
			}),
		},
	}
	e := NewEngine(cfg)
	ret := e.Init()
	require.Equal(t, nvml.SUCCESS, ret, "engine init failed")
	t.Cleanup(func() { _ = e.Shutdown() })

	h, _ := e.DeviceGetHandleByIndex(0)
	dev := e.LookupDevice(h).(*ConfigurableDevice)

	// Pre-trip: identity getters succeed (real NVML answers from the
	// PCI subsystem before the kernel marks the device gone).
	_, ret = dev.GetUUID()
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetUUID")
	_, ret = dev.GetName()
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetName")
	_, ret = dev.GetIndex()
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetIndex")
	_, ret = dev.GetMinorNumber()
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetMinorNumber")
	_, ret = dev.GetPciInfo()
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetPciInfo")
	_, ret = dev.GetBrand()
	require.Equal(t, nvml.SUCCESS, ret, "pre-trip GetBrand")

	// Trip the device with two guarded calls.
	for i := 1; i <= 2; i++ {
		_, _ = dev.GetTemperature(nvml.TEMPERATURE_GPU)
	}

	// Post-trip: every identity getter must surface ERROR_GPU_IS_LOST,
	// matching real NVML behavior where lost GPUs can't even answer
	// identity queries (the kernel driver has marked the device gone).
	tests := []struct {
		name string
		fn   func() nvml.Return
	}{
		{"GetUUID", func() nvml.Return { _, r := dev.GetUUID(); return r }},
		{"GetName", func() nvml.Return { _, r := dev.GetName(); return r }},
		{"GetIndex", func() nvml.Return { _, r := dev.GetIndex(); return r }},
		{"GetMinorNumber", func() nvml.Return { _, r := dev.GetMinorNumber(); return r }},
		{"GetPciInfo", func() nvml.Return { _, r := dev.GetPciInfo(); return r }},
		{"GetBrand", func() nvml.Return { _, r := dev.GetBrand(); return r }},
	}
	for _, tc := range tests {
		require.Equal(t, nvml.ERROR_GPU_IS_LOST, tc.fn(), "post-trip %s", tc.name)
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

	require.NotNil(t, base.Failure, "expected override to replace base.Failure")
	require.Equal(t, FailureModeLost, base.Failure.Mode, "expected override to replace base.Failure with lost mode")
	require.Equal(t, int64(5), base.Failure.AfterCalls, "AfterCalls not propagated")
	require.NotNil(t, base.Failure.Xid, "Xid not propagated")
	require.Equal(t, uint64(79), base.Failure.Xid.Code, "Xid not propagated")
}

// =============================================================================
// Healthy default preserves all existing semantics
// =============================================================================

func TestFailureInjection_HealthyConfigIsNoOp(t *testing.T) {
	dev := newTestDeviceWithConfig(t, withFailure(&FailureInjectionConfig{
		Mode: FailureModeHealthy,
	}))
	require.Nil(t, dev.failure, "healthy mode must not allocate a failureInjector")
	for i := 0; i < 50; i++ {
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		require.Equal(t, nvml.SUCCESS, ret, "call %d", i)
		require.Equal(t, uint32(33), temp, "call %d", i)
	}
}
