// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabricmanager

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// serve runs d until the test ends, returning a func that waits for its exit.
func serve(t *testing.T, d *Daemon) (context.CancelFunc, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel, func() { <-done }
}

func TestServe_PublishesReadiness(t *testing.T) {
	dir := t.TempDir()
	d := NewDaemon(Config{StateDir: dir})

	cancel, wait := serve(t, d)
	eventually(t, func() bool { return d.Ready() }, "readiness never published")
	require.True(t, exists(t, dir))

	cancel()
	wait()
}

// The state dir outlives the pod, so a marker from a previous one must not make
// a fresh node report COMPLETED before it has registered anything.
func TestServe_ClearsStaleMarkerBeforePublishing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteReady(dir))

	// The delay parks Serve before its first write, so what is observed here is
	// the clear on entry.
	d := NewDaemon(Config{StateDir: dir, InitDelay: time.Hour})
	serve(t, d)

	eventually(t, func() bool { return !exists(t, dir) }, "stale marker was not cleared")
	require.False(t, d.Ready())
}

func TestServe_WithholdsReadinessForInitDelay(t *testing.T) {
	dir := t.TempDir()
	d := NewDaemon(Config{StateDir: dir, InitDelay: 250 * time.Millisecond})
	serve(t, d)

	require.False(t, d.Ready(), "readiness must wait out the registration delay")
	eventually(t, func() bool { return d.Ready() }, "readiness never published after the delay")
}

// Anything removing the marker must not strand every GPU on the node at
// IN_PROGRESS for the pod's lifetime.
func TestServe_ReassertsAfterExternalRemoval(t *testing.T) {
	dir := t.TempDir()
	d := NewDaemon(Config{StateDir: dir, ReassertInterval: 20 * time.Millisecond})
	serve(t, d)
	eventually(t, func() bool { return d.Ready() }, "readiness never published")

	require.NoError(t, os.Remove(MarkerPath(dir)))
	eventually(t, func() bool { return exists(t, dir) }, "marker was not re-asserted")
}

func TestServe_ExitsOnCancel(t *testing.T) {
	d := NewDaemon(Config{StateDir: t.TempDir()})
	cancel, wait := serve(t, d)
	eventually(t, func() bool { return d.Ready() }, "readiness never published")

	cancel()
	select {
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not exit on cancellation")
	default:
		wait()
	}
}

func TestStop_WithdrawsReadiness(t *testing.T) {
	dir := t.TempDir()
	d := NewDaemon(Config{StateDir: dir})
	require.NoError(t, WriteReady(dir))

	require.NoError(t, d.Stop())
	require.False(t, exists(t, dir))
	require.False(t, d.Ready())
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}
