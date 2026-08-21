// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane"
	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoints(t *testing.T) {
	cfg := controlplane.DefaultConfig()
	logger := logging.NewLogger(logging.Config{})

	server := controlplane.NewServer(cfg, logger)
	ts := httptest.NewServer(server.Router())
	t.Cleanup(ts.Close)

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Equal(t, http.StatusOK, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "ok\n", string(body))
		})
	}
}

// TestServerShutdownOnContextCancel exercises the full Run/Serve/Shutdown
// lifecycle: bind a real listener, cancel the context, and confirm the server
// drains within its ShutdownTimeout without returning an error.
func TestServerShutdownOnContextCancel(t *testing.T) {
	cfg := controlplane.DefaultConfig()
	cfg.ShutdownTimeout = 500 * time.Millisecond
	logger := logging.NewLogger(logging.Config{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := controlplane.NewServer(cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down within 2s of context cancellation")
	}
}
