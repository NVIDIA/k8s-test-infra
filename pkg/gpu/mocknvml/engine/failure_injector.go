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
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// failureInjector implements GPU failure injection for a single device.
//
// The injector is sticky: once a device trips into the failed state it stays
// failed until the injector is replaced or Reset() is called (runtime
// control), mirroring real "lost" or "fallen off the bus" GPUs that don't
// recover without a reboot. ECC uncorrectable errors are also accumulative on
// real hardware, so the same model applies.
//
// All exported methods are safe for concurrent use. A nil receiver is
// permitted everywhere, in which case the injector reports a healthy
// device — this matches the "no failure config" default.
type failureInjector struct {
	// cfg is set once in newFailureInjector and is never mutated after
	// construction. Reads are therefore safe without synchronization.
	// If the engine ever grows hot-reload of YAML config, replace the
	// whole injector instead of mutating cfg in place.
	cfg *FailureInjectionConfig

	// callCount is incremented on every Tick() and is used to evaluate
	// AfterCalls deterministically. Reads are also done atomically so
	// callers can observe the live count without taking the mutex.
	callCount atomic.Int64

	// tripped flips from false to true exactly once and is then sticky.
	tripped atomic.Bool

	// xidDelivered flips from false to true the first time the configured
	// Xid event is consumed via the NVML event set. Subsequent
	// nvmlEventSetWait calls must not re-deliver the same Xid (real NVML
	// reports each critical Xid exactly once per occurrence). Callers
	// must use ClaimXid to atomically take the event.
	xidDelivered atomic.Bool

	// rng is only read/written while holding mu. We avoid a fast-path that
	// would race here because Probability rolls happen at most once per
	// guarded call until the device trips, then never again.
	mu  sync.Mutex
	rng *rand.Rand
}

// newFailureInjector returns nil when cfg is nil or describes a healthy
// device — that nil receiver is the signal callers use to skip all
// failure-injection bookkeeping. A non-nil injector is returned only when
// the config selects an actual failure mode.
func newFailureInjector(cfg *FailureInjectionConfig) *failureInjector {
	if cfg == nil {
		return nil
	}
	if normalizedMode(cfg.Mode) == FailureModeHealthy {
		return nil
	}

	seed1 := uint64(cfg.Seed)
	if seed1 == 0 {
		seed1 = uint64(time.Now().UnixNano())
	}
	// Same scheme as dynamicMetricsSimulator: derive the second PCG word
	// from the first so a single user-supplied seed gives reproducible
	// rolls within a run.
	seed2 := seed1 ^ 0x9E3779B97F4A7C15

	return &failureInjector{
		cfg: cfg,
		rng: rand.New(rand.NewPCG(seed1, seed2)),
	}
}

// Mode returns the normalized failure mode. Unknown / empty values are
// reported as FailureModeHealthy so callers can switch on a small set.
func (f *failureInjector) Mode() string {
	if f == nil || f.cfg == nil {
		return FailureModeHealthy
	}
	return normalizedMode(f.cfg.Mode)
}

// Triggered reports whether the failure is currently active. It does not
// roll the dice or advance the call counter — use Tick for that.
func (f *failureInjector) Triggered() bool {
	if f == nil {
		return false
	}
	return f.tripped.Load()
}

// Tick records one guarded NVML call and may flip the device into the
// failed state. It always returns the latest Triggered() value so callers
// can `if f.Tick() { return f.ErrorReturn() }`.
//
// Activation rules (in order):
//  1. Already tripped → stays tripped.
//  2. Neither Probability nor AfterCalls set → trips on the very first
//     call so a bare `failure: { mode: lost }` block produces an
//     immediately-lost device.
//  3. AfterCalls > 0 and callCount >= AfterCalls → trip.
//  4. Probability > 0 and a uniform sample lands below it → trip.
func (f *failureInjector) Tick() bool {
	if f == nil {
		return false
	}
	// Always increment so consumers (e.g. ecc_uncorrectable mode) can
	// surface a strictly-monotonic post-trip counter and so concurrency
	// tests can assert on the total Tick call volume.
	count := f.callCount.Add(1)
	if f.tripped.Load() {
		return true
	}

	// Mode set without any trigger → fail immediately. Treat the very
	// first observed call as the trigger so consumers see the failure on
	// the first guarded API call rather than at engine init time, which
	// would race with handle registration.
	if f.cfg.Probability <= 0 && f.cfg.AfterCalls <= 0 {
		f.tripped.Store(true)
		return true
	}

	if f.cfg.AfterCalls > 0 && count >= f.cfg.AfterCalls {
		f.tripped.Store(true)
		return true
	}

	if f.cfg.Probability > 0 {
		f.mu.Lock()
		roll := f.rng.Float64()
		f.mu.Unlock()
		if roll < f.cfg.Probability {
			f.tripped.Store(true)
			return true
		}
	}

	return false
}

// ErrorReturn maps the configured failure mode to the NVML return code
// callers should surface to NVML clients once the device has tripped.
//
// "lost" and "fallen_off_bus" both map to ERROR_GPU_IS_LOST — that is the
// kernel-level error real consumers actually observe; the distinction
// only matters at the Xid level (typically 79 vs 74) and is handled by
// Xid().
//
// "ecc_uncorrectable" returns SUCCESS because the failure manifests as
// non-zero ECC counters and a Xid event rather than a failed API call.
func (f *failureInjector) ErrorReturn() nvml.Return {
	switch f.Mode() {
	case FailureModeLost, FailureModeFallenOffBus:
		return nvml.ERROR_GPU_IS_LOST
	default:
		return nvml.SUCCESS
	}
}

// IsLost returns true when the device has tripped and the configured mode
// indicates the GPU should appear missing on the bus. This is the
// condition handle-lookup paths consult before returning a handle.
func (f *failureInjector) IsLost() bool {
	if !f.Triggered() {
		return false
	}
	switch f.Mode() {
	case FailureModeLost, FailureModeFallenOffBus:
		return true
	default:
		return false
	}
}

// IsECCUncorrectable returns true when the device has tripped into the
// ecc_uncorrectable mode. Callers use this to decide whether ECC counters
// should report non-zero errors.
func (f *failureInjector) IsECCUncorrectable() bool {
	return f.Triggered() && f.Mode() == FailureModeECCUncorrectable
}

// Xid returns the Xid error code to surface, or 0 when no Xid was
// configured or the device has not tripped yet. Callers use this to
// peek at the configured Xid; to atomically consume the event for
// delivery via the NVML event set use ClaimXid instead.
func (f *failureInjector) Xid() uint64 {
	if !f.Triggered() {
		return 0
	}
	if f.cfg == nil || f.cfg.Xid == nil {
		return 0
	}
	return f.cfg.Xid.Code
}

// ClaimXid atomically claims the pending Xid event for delivery to a
// caller (the bridge's nvmlEventSetWait). It returns the Xid code and
// true on the first call after the device trips with a Xid configured;
// every subsequent call returns (0, false) so consumers don't see the
// same Xid replay forever.
func (f *failureInjector) ClaimXid() (uint64, bool) {
	xid := f.Xid()
	if xid == 0 {
		return 0, false
	}
	if !f.xidDelivered.CompareAndSwap(false, true) {
		return 0, false
	}
	return xid, true
}

// Reset returns the injector to its untripped state. It is used by runtime
// control (nvml-mock-ctl reset / mode healthy) to recover a device without a
// process restart. Callers that want a genuinely healthy device should drop
// the injector entirely (set it to nil); Reset exists for the case where the
// same injector object is reused. Safe on a nil receiver.
func (f *failureInjector) Reset() {
	if f == nil {
		return
	}
	f.tripped.Store(false)
	f.xidDelivered.Store(false)
	f.callCount.Store(0)
}

// CallCount returns the number of Tick()s observed so far. Exposed for
// tests and diagnostic logging.
func (f *failureInjector) CallCount() int64 {
	if f == nil {
		return 0
	}
	return f.callCount.Load()
}

// normalizedMode coerces config strings into one of the FailureMode*
// constants. Unknown values resolve to FailureModeHealthy so a typo in
// the config never surprises consumers with an unexpected error code.
func normalizedMode(m string) string {
	switch m {
	case FailureModeLost, FailureModeFallenOffBus, FailureModeECCUncorrectable:
		return m
	default:
		return FailureModeHealthy
	}
}
