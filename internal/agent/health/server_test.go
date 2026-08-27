// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestServer() *Server {
	return NewServer(":0", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestHealthz_LivenessDefault(t *testing.T) {
	w := get(t, newTestServer().Handler(), "/healthz")
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHealthz_LivenessFalse(t *testing.T) {
	srv := newTestServer()
	srv.SetLiveness(func() bool { return false })

	w := get(t, srv.Handler(), "/healthz")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp HealthzResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.False(t, resp.OK)
}

func TestReadyz_AllReady(t *testing.T) {
	srv := newTestServer()
	srv.SetReadiness(func() map[string]bool {
		return map[string]bool{"sim-a": true, "sim-b": true}
	})

	w := get(t, srv.Handler(), "/readyz")
	require.Equal(t, http.StatusOK, w.Code)

	var resp ReadyzResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.True(t, resp.OK)
	require.True(t, resp.Simulators["sim-a"].OK)
	require.True(t, resp.Simulators["sim-b"].OK)
}

func TestReadyz_OneNotReady(t *testing.T) {
	srv := newTestServer()
	srv.SetReadiness(func() map[string]bool {
		return map[string]bool{"sim-a": true, "sim-b": false}
	})

	w := get(t, srv.Handler(), "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp ReadyzResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.False(t, resp.OK)
	require.False(t, resp.Simulators["sim-b"].OK)
}
