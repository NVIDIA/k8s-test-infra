// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The states this file pins down, and why each matters. A probe that only
// proves the process is alive reports healthy in states 2, 4 and 5 — the
// process keeps running in every one of them.
//
//	state 1  process dead                       -> the kubelet already handles it
//	state 2  alive, never registered            -> must be NOT READY
//	state 3  alive, registered, serving         -> READY
//	state 4  alive, registered, handler wedged  -> NOT READY and NOT LIVE
//	state 5  alive, unregistered by containerd  -> must be NOT READY
//
// The clock is injected so wedge detection is asserted against exact
// durations rather than by sleeping.

// fakeTimeouts stands in for the NRI stub as the source of the runtime's
// request timeout. The real value arrives from containerd in the Configure
// request, so it is not a constant the plugin controls.
type fakeTimeouts struct{ requestTimeout time.Duration }

func (f fakeTimeouts) RequestTimeout() time.Duration { return f.requestTimeout }

// testClock is a manually advanced clock.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestHealth builds a health tracker whose wedge threshold is a known
// 2 x 4s = 8s, so every duration assertion below is a literal.
func newTestHealth(t *testing.T) (*health, *testClock) {
	t.Helper()
	clock := &testClock{t: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
	h := newHealth(clock.now, wedgeFactor)
	h.setTimeoutSource(fakeTimeouts{requestTimeout: 4 * time.Second})
	return h, clock
}

// State 2. Catches the probe-shaped no-op: a readiness surface that reports
// ready the moment the HTTP listener binds, before containerd has registered
// the plugin. Pods created in that window are silently not injected.
func TestNotReadyBeforeRegistration(t *testing.T) {
	t.Parallel()

	h, _ := newTestHealth(t)

	require.False(t, h.readiness().ok, "must not be ready before Configure")
	require.True(t, h.liveness().ok, "must stay live while waiting to register")
}

// State 3.
func TestReadyOnceRegistered(t *testing.T) {
	t.Parallel()

	h, _ := newTestHealth(t)
	h.setRegistered(true)

	require.True(t, h.readiness().ok)
	require.True(t, h.liveness().ok)
}

// State 5. containerd dropped the connection and the plugin is no longer
// receiving CreateContainer calls. Injection has silently stopped: every pod
// created from now on runs unmocked. Readiness must surface it.
func TestNotReadyAfterRuntimeDisconnects(t *testing.T) {
	t.Parallel()

	h, _ := newTestHealth(t)
	h.setRegistered(true)
	require.True(t, h.readiness().ok, "precondition: registered")

	h.setRegistered(false) // what the stub's OnClose callback does

	require.False(t, h.readiness().ok, "must not be ready once the runtime disconnects")
}

// A request that is in flight but has not yet exceeded the runtime's own
// timeout is ordinary traffic. Catches a wedge detector tuned so tight that
// every normal container creation restarts the plugin.
func TestInFlightRequestUnderTimeoutStaysHealthy(t *testing.T) {
	t.Parallel()

	h, clock := newTestHealth(t)
	h.setRegistered(true)

	done := h.begin()
	clock.advance(7 * time.Second) // threshold is 8s

	require.True(t, h.liveness().ok, "a request under the wedge threshold is not a wedge")
	require.True(t, h.readiness().ok)

	done()
	require.True(t, h.liveness().ok)
}

// State 4 — the failure the issue is about. The process is alive, the ttRPC
// connection is up, and the handler is not coming back. containerd has already
// abandoned this request. Only a liveness failure recovers the node.
func TestWedgedRequestFailsLivenessAndReadiness(t *testing.T) {
	t.Parallel()

	h, clock := newTestHealth(t)
	h.setRegistered(true)

	h.begin() // deliberately never completed
	clock.advance(9 * time.Second)

	require.False(t, h.liveness().ok, "an in-flight request past the wedge threshold must fail liveness")
	require.False(t, h.readiness().ok, "a wedged plugin is not serving")
}

// Catches a latch bug: a wedge flag that is set but never cleared leaves the
// plugin permanently unhealthy after one slow request, restarting it forever.
func TestWedgeClearsWhenTheRequestCompletes(t *testing.T) {
	t.Parallel()

	h, clock := newTestHealth(t)
	h.setRegistered(true)

	done := h.begin()
	clock.advance(9 * time.Second)
	require.False(t, h.liveness().ok, "precondition: wedged")

	done()

	require.True(t, h.liveness().ok, "liveness must recover once the slow request finishes")
	require.True(t, h.readiness().ok)
}

// The discriminating test against the obvious wrong design. A "time since the
// last request" watchdog reports wedged on any node that simply is not
// creating containers, which is the normal steady state. Idle is not wedged.
func TestIdleAfterRegistrationIsNotWedged(t *testing.T) {
	t.Parallel()

	h, clock := newTestHealth(t)
	h.setRegistered(true)

	done := h.begin()
	done()
	clock.advance(72 * time.Hour)

	require.True(t, h.liveness().ok, "an idle plugin is healthy; only in-flight work can wedge")
	require.True(t, h.readiness().ok)
}

// Concurrent requests: the oldest one decides. Catches a detector that only
// tracks the most recent request and so loses the stuck one behind later
// traffic.
func TestOldestInFlightRequestDecides(t *testing.T) {
	t.Parallel()

	h, clock := newTestHealth(t)
	h.setRegistered(true)

	h.begin() // stuck, never completes
	clock.advance(9 * time.Second)
	done := h.begin() // a later, healthy request
	done()

	require.False(t, h.liveness().ok, "a newer completed request must not mask the stuck one")
}

// The threshold is derived from what containerd reported, not hardcoded. A
// runtime configured with a longer plugin_request_timeout must not have its
// plugin restarted out from under it.
func TestWedgeThresholdFollowsTheRuntimeRequestTimeout(t *testing.T) {
	t.Parallel()

	h, clock := newTestHealth(t)
	h.setRegistered(true)
	h.setTimeoutSource(fakeTimeouts{requestTimeout: time.Minute})

	h.begin()
	clock.advance(90 * time.Second) // past the 8s default, under 2 x 60s

	require.True(t, h.liveness().ok, "threshold must follow the runtime's request timeout")

	clock.advance(45 * time.Second) // now past 2 x 60s

	require.False(t, h.liveness().ok)
}

// Before Configure lands there is no runtime-reported timeout, so the stub
// default applies. Catches a zero threshold, which would make every in-flight
// request look wedged.
func TestWedgeThresholdFallsBackBeforeConfigure(t *testing.T) {
	t.Parallel()

	clock := &testClock{t: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
	h := newHealth(clock.now, wedgeFactor)

	h.begin()
	clock.advance(time.Millisecond)
	require.True(t, h.liveness().ok, "a just-started request must not read as wedged")

	clock.advance(24 * time.Hour)
	require.False(t, h.liveness().ok, "a request stuck for a day is wedged under any timeout")
}

func TestHealthEndpointStatusCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		setup      func(*health, *testClock)
		wantStatus int
	}{
		{
			name:       "readyz before registration",
			path:       "/readyz",
			setup:      func(*health, *testClock) {},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "readyz once registered",
			path:       "/readyz",
			setup:      func(h *health, _ *testClock) { h.setRegistered(true) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "healthz before registration",
			path:       "/healthz",
			setup:      func(*health, *testClock) {},
			wantStatus: http.StatusOK,
		},
		{
			name: "readyz after the runtime disconnects",
			path: "/readyz",
			setup: func(h *health, _ *testClock) {
				h.setRegistered(true)
				h.setRegistered(false)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "healthz while wedged",
			path: "/healthz",
			setup: func(h *health, c *testClock) {
				h.setRegistered(true)
				h.begin()
				c.advance(9 * time.Second)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "readyz while wedged",
			path: "/readyz",
			setup: func(h *health, c *testClock) {
				h.setRegistered(true)
				h.begin()
				c.advance(9 * time.Second)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, clock := newTestHealth(t)
			tt.setup(h, clock)

			rec := httptest.NewRecorder()
			h.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			require.Equal(t, tt.wantStatus, rec.Code)
			require.NotEmpty(t, rec.Body.String(), "probe body should say why, for kubectl describe")
		})
	}
}

// An unknown path must not report 200. Catches a catch-all handler that would
// make any probe path — including a typo in the chart — look healthy forever.
func TestUnknownHealthPathIsNotOK(t *testing.T) {
	t.Parallel()

	h, _ := newTestHealth(t)
	h.setRegistered(true)

	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthzz", nil))

	require.Equal(t, http.StatusNotFound, rec.Code)
}
