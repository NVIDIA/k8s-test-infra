// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/stretchr/testify/require"
)

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) health.Probe {
	t.Helper()
	var probe health.Probe
	require.NoError(t, json.NewDecoder(w.Body).Decode(&probe))
	return probe
}

func TestZeroProbeFailsClosed(t *testing.T) {
	t.Parallel()

	w := get(t, health.Handler(func() health.Probe { return health.Probe{} }), "/healthz")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestProbeReasonIsServedInTheBody(t *testing.T) {
	t.Parallel()

	handler := health.Handler(func() health.Probe { return health.Unhealthy("wedged for %s", "3s") })

	w := get(t, handler, "/healthz")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "wedged for 3s", decode(t, w).Reason)
}

func TestProbesPassBeforeChecksAreSet(t *testing.T) {
	t.Parallel()

	srv := health.NewServer(":0", time.Second)
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, http.StatusOK, get(t, srv.Handler(), path).Code)
		})
	}
}

func TestChecksSetAfterHandlerStillTakeEffect(t *testing.T) {
	t.Parallel()

	srv := health.NewServer(":0", time.Second)
	handler := srv.Handler()
	srv.SetLiveness(func() health.Probe { return health.Unhealthy("stage failed: ib") })

	w := get(t, handler, "/healthz")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "stage failed: ib", decode(t, w).Reason)
}

func TestReadyzReportsComponentAttribution(t *testing.T) {
	t.Parallel()

	srv := health.NewServer(":0", time.Second)
	srv.SetReadiness(func() health.Probe {
		return health.Probe{Components: map[string]health.Probe{
			"gpudriver": health.OK(),
			"ib":        health.Unhealthy("daemon not listening"),
		}}
	})

	w := get(t, srv.Handler(), "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	probe := decode(t, w)
	require.True(t, probe.Components["gpudriver"].OK)
	require.False(t, probe.Components["ib"].OK)
	require.Equal(t, "daemon not listening", probe.Components["ib"].Reason)
}

// An empty address is how a deployment opts out of the probe surface, so Run
// must return rather than fail to bind "".
func TestRunWithoutAnAddressIsANoOp(t *testing.T) {
	t.Parallel()

	require.NoError(t, health.NewServer("", time.Second).Run(context.Background()))
}

func TestRunFailsLoudlyOnAPortClash(t *testing.T) {
	t.Parallel()

	held := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(held.Close)

	err := health.NewServer(held.Listener.Addr().String(), time.Second).Run(context.Background())
	require.ErrorContains(t, err, "listen on")
}

func TestRunServesUntilContextCancellation(t *testing.T) {
	t.Parallel()

	srv := health.NewServer("127.0.0.1:0", 500*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("health server did not shut down within 2s of context cancellation")
	}
}
