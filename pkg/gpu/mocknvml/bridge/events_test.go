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

package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// schedulerSlack absorbs wakeup jitter on a loaded CI machine. Every timing
// assertion below is one-sided, so this need not encode precise timing.
const schedulerSlack = 500 * time.Millisecond

// An already-pending Xid must not be delayed by the poll interval.
func TestWaitForDelivery_DeliversPendingEventImmediately(t *testing.T) {
	var calls atomic.Int32
	start := time.Now()
	delivered := waitForDelivery(5*time.Second, time.Second, func() bool {
		calls.Add(1)
		return true
	})
	elapsed := time.Since(start)

	require.True(t, delivered, "pending event must be reported as delivered")
	require.Equal(t, int32(1), calls.Load(), "pending event must be taken on the first poll")
	require.Less(t, elapsed, time.Second, "a pending event must not wait for the poll interval")
}

// Regression guard for the busy-spin that pinned a CPU core in
// nvidia-device-plugin: an idle wait must consume the whole timeout, no more,
// with a bounded number of polls.
func TestWaitForDelivery_BlocksForRequestedTimeoutWhenIdle(t *testing.T) {
	const (
		timeout      = 200 * time.Millisecond
		pollInterval = 20 * time.Millisecond
	)

	var calls atomic.Int32
	start := time.Now()
	delivered := waitForDelivery(timeout, pollInterval, func() bool {
		calls.Add(1)
		return false
	})
	elapsed := time.Since(start)

	require.False(t, delivered, "no event was pending, so nothing may be delivered")
	require.GreaterOrEqual(t, elapsed, timeout, "an idle wait must block for the full timeout")
	require.Less(t, elapsed, timeout+schedulerSlack,
		"an idle wait must return at the timeout, not block past it")

	// One poll per interval, plus the fast-path poll and a short final sleep.
	maxCalls := int32(timeout/pollInterval) + 3
	require.LessOrEqual(t, calls.Load(), maxCalls,
		"idle wait polled %d times (max %d) — it is spinning instead of sleeping",
		calls.Load(), maxCalls)
}

// Fault injection must stay responsive: the event is raised while the wait is
// already parked, and latency is measured from the injection itself.
func TestWaitForDelivery_DeliversWithinOnePollOfInjection(t *testing.T) {
	const (
		timeout      = 30 * time.Second
		pollInterval = 20 * time.Millisecond
		injectAfter  = 100 * time.Millisecond
	)

	var pending atomic.Bool
	var injectedAtNanos atomic.Int64

	go func() {
		time.Sleep(injectAfter)
		// Timestamp before visibility, so latency can never be negative.
		injectedAtNanos.Store(time.Now().UnixNano())
		pending.Store(true)
	}()

	delivered := waitForDelivery(timeout, pollInterval, pending.Load)
	returnedAt := time.Now()

	require.True(t, delivered, "an event injected mid-wait must be delivered")
	injectedAt := injectedAtNanos.Load()
	require.NotZero(t, injectedAt, "event must have been injected while the wait was parked")

	latency := returnedAt.Sub(time.Unix(0, injectedAt))
	require.LessOrEqual(t, latency, pollInterval+schedulerSlack,
		"delivery took %s after injection; must be within one poll interval", latency)
}

// As in real NVML, a zero timeout means "check and return", not "block".
func TestWaitForDelivery_ZeroTimeoutIsNonBlockingPoll(t *testing.T) {
	var calls atomic.Int32
	start := time.Now()
	delivered := waitForDelivery(0, time.Second, func() bool {
		calls.Add(1)
		return false
	})
	elapsed := time.Since(start)

	require.False(t, delivered)
	require.Equal(t, int32(1), calls.Load(), "zero timeout must poll exactly once")
	require.Less(t, elapsed, schedulerSlack, "zero timeout must not block")
}

// A NULL payload must fail fast with INVALID_ARGUMENT, not wait out the
// timeout and report TIMEOUT.
func TestEventSetWait_RejectsNilEventData(t *testing.T) {
	for _, tc := range []struct {
		version string
		wait    func() uint32
	}{
		{"v1", func() uint32 { return uint32(nvmlEventSetWait_v1(nil, nil, 10_000)) }},
		{"v2", func() uint32 { return uint32(nvmlEventSetWait_v2(nil, nil, 10_000)) }},
	} {
		t.Run(tc.version, func(t *testing.T) {
			start := time.Now()
			ret := tc.wait()
			elapsed := time.Since(start)

			require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), ret,
				"NULL event data must be rejected")
			require.Less(t, elapsed, schedulerSlack,
				"an invalid call must not wait out the timeout")
		})
	}
}

// Exercises the exported entry points, pinning the millisecond conversion and
// NVML status codes that the waitForDelivery tests above do not see.
func TestEventSetWait_BlocksForRequestedMilliseconds(t *testing.T) {
	const timeout = 150 * time.Millisecond

	for _, tc := range []struct {
		version string
		wait    func(timeoutms uint32) uint32
	}{
		{"v1", eventSetWaitV1ForTest},
		{"v2", eventSetWaitV2ForTest},
	} {
		t.Run(tc.version, func(t *testing.T) {
			start := time.Now()
			ret := tc.wait(uint32(timeout.Milliseconds()))
			elapsed := time.Since(start)

			require.Equal(t, uint32(nvml.ERROR_TIMEOUT), ret,
				"no Xid is pending, so the wait must report TIMEOUT")
			require.GreaterOrEqual(t, elapsed, timeout,
				"timeoutms must be interpreted as milliseconds and honored")
			require.Less(t, elapsed, timeout+schedulerSlack,
				"the wait must return at the timeout, not block past it")
		})
	}
}
