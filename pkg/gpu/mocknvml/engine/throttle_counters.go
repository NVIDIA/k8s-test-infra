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

// Cumulative clocks-event (throttle) time, per cause. The instantaneous
// clocks_throttle_reasons flags say whether the GPU is being held back right
// now; these counters say how long it has been held back since reset, which is
// how throttling is diagnosed after a workload has already run slow.
//
// nvidia-smi renders them as the "Clocks Event Reasons Counters" block, reading
// them through nvmlDeviceGetFieldValues rather than any dedicated getter — an
// unhandled field id there comes back per-field NOT_SUPPORTED and renders as
// N/A, so the counters must be answered even when the answer is zero.

package engine

import (
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// NVML field ids for the throttle counters. All carry nanoseconds.
//
// Two causes have long-standing ids in the perf-policy family, which
// NVML_FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP and _SYNC_BOOST are defined as;
// NVML_FI_DEV_PERF_POLICY_THERMAL is the older name for the SW thermal counter.
// The three slowdown causes only exist under the newer names.
//
// Those three are NOT taken from the header this repo vendors, which is a CUDA
// 13 header numbering them 269-271 and reusing 251-253 for power smoothing.
// The driver these profiles report — and the nvidia-smi 580.65.06 the image
// bundles — has them at 251-253 and power smoothing at 256-273 instead. The
// simulated driver's numbering is the one consumers see, so it is the one used
// here; verified by tracing a `nvidia-smi -q` field-value batch, which asks for
// exactly 74, 76, 251, 252 and 253 at the whole-GPU scope.
const (
	fiPerfPolicyPower     = 74
	fiPerfPolicyThermal   = 75
	fiPerfPolicySyncBoost = 76

	fiClocksEventSwThermSlowdown = 251
	fiClocksEventHwThermSlowdown = 252
	fiClocksEventHwPowerBrake    = 253
)

// throttleCause enumerates the causes that accrue time independently. These are
// exactly the five rows nvidia-smi prints, which is also the granularity NVML
// exposes: the umbrella "HW Slowdown" flag has no counter of its own.
type throttleCause int

const (
	throttleCauseSWPowerCap throttleCause = iota
	throttleCauseSyncBoost
	throttleCauseSWThermal
	throttleCauseHWThermal
	throttleCauseHWPowerBrake
	throttleCauseCount
)

// throttleAccrual tracks how long each cause has been active on one device.
// Time is folded in on read rather than by a background ticker: a mock has no
// reason to burn a goroutine per device, and a counter nobody reads has no
// observable value.
type throttleAccrual struct {
	device *ConfigurableDevice

	// now is overridable in tests so accrual can be driven deterministically
	// without sleeping.
	now func() time.Time

	mu sync.Mutex
	// accrued is the time each cause has been observed active for, and
	// activeSince is when it was last observed (zero == currently inactive).
	// A read advances accrued to the moment of that read, so a cause the mock
	// never saw active contributes nothing and the totals never go backwards.
	accrued     [throttleCauseCount]time.Duration
	activeSince [throttleCauseCount]time.Time
}

// throttleAccrual returns the device's accrual state, creating it on first use.
// Lazy creation keeps every device-construction path (including the legacy
// no-YAML one) from having to know about it.
func (d *ConfigurableDevice) throttleAccrual() *throttleAccrual {
	if a := d.throttle.Load(); a != nil {
		return a
	}
	fresh := &throttleAccrual{device: d, now: time.Now}
	if d.throttle.CompareAndSwap(nil, fresh) {
		return fresh
	}
	return d.throttle.Load()
}

// resolve returns the per-cause totals in nanoseconds: the configured baseline
// plus the time each cause has been active for.
//
// Accrual advances only between reads, because a read is the only moment the
// mock knows the flag's state. A cause cleared between two reads therefore
// stops at the last read that saw it active rather than being credited with the
// whole gap — the counters report time observed throttled, never time assumed.
func (a *throttleAccrual) resolve() [throttleCauseCount]uint64 {
	cfg := a.device.cfg().ClocksThrottleReasons
	active := activeThrottleCauses(cfg)
	baseline := throttleCauseBaselines(cfg)

	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	var out [throttleCauseCount]uint64
	for cause := range out {
		switch {
		case !active[cause]:
			a.activeSince[cause] = time.Time{}
		case !a.activeSince[cause].IsZero():
			a.accrued[cause] += now.Sub(a.activeSince[cause])
			a.activeSince[cause] = now
		default:
			a.activeSince[cause] = now
		}
		out[cause] = uint64(baseline[cause] + a.accrued[cause])
	}
	return out
}

// activeThrottleCauses reports which causes the instantaneous flags currently
// assert. The umbrella hw_slowdown flag is deliberately not mapped: it is the
// disjunction the two hardware causes below it drive, so counting it would
// double-count them.
func activeThrottleCauses(cfg *ClocksThrottleReasonsConfig) [throttleCauseCount]bool {
	var active [throttleCauseCount]bool
	if cfg == nil {
		return active
	}
	active[throttleCauseSWPowerCap] = cfg.SWPowerCap
	active[throttleCauseSyncBoost] = cfg.SyncBoost
	active[throttleCauseSWThermal] = cfg.SWThermalSlowdown
	active[throttleCauseHWThermal] = cfg.HWThermalSlowdown
	active[throttleCauseHWPowerBrake] = cfg.HWPowerBrakeSlowdown
	return active
}

// throttleCauseBaselines converts the configured microsecond totals into
// durations. An absent counters block means a GPU that has never been
// throttled, which is a real answer of zero.
func throttleCauseBaselines(cfg *ClocksThrottleReasonsConfig) [throttleCauseCount]time.Duration {
	var baseline [throttleCauseCount]time.Duration
	if cfg == nil || cfg.Counters == nil {
		return baseline
	}
	c := cfg.Counters
	baseline[throttleCauseSWPowerCap] = time.Duration(c.SWPowerCapUS) * time.Microsecond
	baseline[throttleCauseSyncBoost] = time.Duration(c.SyncBoostUS) * time.Microsecond
	baseline[throttleCauseSWThermal] = time.Duration(c.SWThermalSlowdownUS) * time.Microsecond
	baseline[throttleCauseHWThermal] = time.Duration(c.HWThermalSlowdownUS) * time.Microsecond
	baseline[throttleCauseHWPowerBrake] = time.Duration(c.HWPowerBrakeSlowdownUS) * time.Microsecond
	return baseline
}

// throttleCounterFieldValue resolves the throttle-counter field ids. The fourth
// return says whether the id belongs to this set, matching the convention in
// getDeviceFieldValue.
func (d *ConfigurableDevice) throttleCounterFieldValue(fieldID uint32) (FieldValueType, uint64, nvml.Return, bool) {
	cause, ok := throttleCauseForField(fieldID)
	if !ok {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, false
	}
	return FieldValueUint64, d.throttleAccrual().resolve()[cause], nvml.SUCCESS, true
}

func throttleCauseForField(fieldID uint32) (throttleCause, bool) {
	switch fieldID {
	case fiPerfPolicyPower:
		return throttleCauseSWPowerCap, true
	case fiPerfPolicySyncBoost:
		return throttleCauseSyncBoost, true
	case fiPerfPolicyThermal, fiClocksEventSwThermSlowdown:
		return throttleCauseSWThermal, true
	case fiClocksEventHwThermSlowdown:
		return throttleCauseHWThermal, true
	case fiClocksEventHwPowerBrake:
		return throttleCauseHWPowerBrake, true
	default:
		return 0, false
	}
}

// GetViolationStatus returns how long one performance policy has held the GPU
// below its requested clocks. It is the deprecated getter for the same state
// the throttle-counter field ids carry, so for the three policies NVML maps
// onto a clocks-event cause both paths resolve from one accrual and cannot
// disagree.
//
// The board-limit, low-utilization, reliability and total-base-clocks limiters
// are not modelled, but they are real policies the header maps onto their own
// field ids, so they report zero rather than declining: zero is the answer for
// a limiter that has never fired, where NOT_SUPPORTED says the driver cannot
// tell — the same distinction that made these counters read N/A in the first
// place. Only NVML_PERF_POLICY_TOTAL_APP_CLOCKS declines, because the header
// marks it "DEPRECATED, Do not use" rather than giving it a field id.
//
// Failure injection does not overload these fields with the configured Xid
// code — that is surfaced through the NVML event set — but a device tripped
// into a lost state still returns ERROR_GPU_IS_LOST like every guarded getter.
func (d *ConfigurableDevice) GetViolationStatus(perfPolicyType nvml.PerfPolicyType) (nvml.ViolationTime, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.ViolationTime{}, ret
	}

	switch perfPolicyType {
	case nvml.PERF_POLICY_POWER, nvml.PERF_POLICY_THERMAL, nvml.PERF_POLICY_SYNC_BOOST,
		nvml.PERF_POLICY_BOARD_LIMIT, nvml.PERF_POLICY_LOW_UTILIZATION,
		nvml.PERF_POLICY_RELIABILITY, nvml.PERF_POLICY_TOTAL_BASE_CLOCKS:
	case nvml.PERF_POLICY_TOTAL_APP_CLOCKS:
		debugLog("[NVML] nvmlDeviceGetViolationStatus(policy=%d) -> NOT_SUPPORTED (deprecated policy)\n",
			perfPolicyType)
		return nvml.ViolationTime{}, nvml.ERROR_NOT_SUPPORTED
	default:
		// The enum has gaps (6-9) as well as an upper bound, and neither is a
		// policy a driver would answer.
		debugLog("[NVML] nvmlDeviceGetViolationStatus(policy=%d) -> INVALID_ARGUMENT\n", perfPolicyType)
		return nvml.ViolationTime{}, nvml.ERROR_INVALID_ARGUMENT
	}

	accrual := d.throttleAccrual()
	var violation uint64
	if cause, modelled := throttleCauseForPolicy(perfPolicyType); modelled {
		violation = accrual.resolve()[cause]
	}
	debugLog("[NVML] nvmlDeviceGetViolationStatus(policy=%d) -> %d ns\n", perfPolicyType, violation)
	return nvml.ViolationTime{
		// NVML documents referenceTime as a CPU timestamp in microseconds,
		// unlike violationTime which is nanoseconds.
		ReferenceTime: uint64(accrual.now().UnixMicro()),
		ViolationTime: violation,
	}, nvml.SUCCESS
}

// throttleCauseForPolicy maps the performance policies that have a clocks-event
// counterpart, per the translation table in nvml.h. false means a policy the
// mock does not model, which reports zero rather than declining.
func throttleCauseForPolicy(policy nvml.PerfPolicyType) (throttleCause, bool) {
	switch policy {
	case nvml.PERF_POLICY_POWER:
		return throttleCauseSWPowerCap, true
	case nvml.PERF_POLICY_THERMAL:
		return throttleCauseSWThermal, true
	case nvml.PERF_POLICY_SYNC_BOOST:
		return throttleCauseSyncBoost, true
	default:
		return 0, false
	}
}
