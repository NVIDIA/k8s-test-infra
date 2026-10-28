// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A runtime that is down, as containerd is while the container toolkit
// restarts it, keeps the plugin retrying in-process rather than exiting into
// the kubelet's crash backoff.
func TestRunRetriesWhileTheRuntimeIsDown(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "nri.sock")
	plugin := NewPlugin(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*reregisterInterval)
	defer cancel()

	start := time.Now()
	require.NoError(t, plugin.Run(ctx))

	require.GreaterOrEqual(t, time.Since(start), 3*reregisterInterval, "Run returned before it was stopped")
	require.False(t, plugin.Readiness().OK, "not registered while the runtime is down")
}

// A configuration the stub rejects never registers, so the plugin exits with
// the reason instead of retrying it as if the runtime were down.
func TestRunReturnsAConfigurationTheStubRejects(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "nri.sock")
	cfg.PluginIndex = "x"
	ctx, cancel := context.WithTimeout(context.Background(), 10*reregisterInterval)
	defer cancel()

	err := NewPlugin(cfg).Run(ctx)

	require.ErrorIs(t, err, errCreateStub)
	require.NoError(t, ctx.Err(), "Run retried until it was stopped")
}

// A runtime that stays down warns once a minute, not on every attempt.
func TestReregisterWarningsAreThrottled(t *testing.T) {
	t.Parallel()

	var warnings throttle
	start := time.Now()

	require.True(t, warnings.allow(start), "the first failure warns")
	require.False(t, warnings.allow(start.Add(reregisterInterval)))
	require.False(t, warnings.allow(start.Add(reregisterWarnInterval-time.Millisecond)))
	require.True(t, warnings.allow(start.Add(reregisterWarnInterval)))
}
