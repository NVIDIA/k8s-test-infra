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

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fabricmanager"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func enabledState() *agent.State {
	return &agent.State{Fabric: agent.FabricState{ManagerStateDir: fabricmanager.DefaultStateDir}}
}

func markerPath(h *host.Host) string { return fabricmanager.MarkerPath(h.RootPath(stateDirRel)) }

// The chart sets the state dir only where it also mounts it, so an empty value
// means no fabricmanager on this node.
func TestStage_NoOpWithoutStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))
	require.True(t, s.Ready(), "a node without fabricmanager is ready, not pending")
	require.NoDirExists(t, h.RootPath(stateDirRel))
}

func TestStage_CreatesStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	require.DirExists(t, h.RootPath(stateDirRel))
	require.False(t, s.Ready(), "readiness waits for Run to publish the marker")
}

func TestRun_PublishesAndHoldsReadiness(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	requireEventually(t, func() bool { return s.Ready() }, "marker never published")
	require.FileExists(t, markerPath(h))

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on cancellation")
	}
}

// The hostPath outlives the pod, so a marker from a previous one must not make
// a fresh pod report COMPLETED before it has registered anything.
func TestRun_ClearsStaleMarkerBeforePublishing(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{InitDelay: time.Hour})
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	require.NoError(t, fabricmanager.WriteReady(h.RootPath(stateDirRel)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	// The init delay parks Run before it writes, so anything left is the marker
	// it cleared on entry.
	requireEventually(t, func() bool { _, err := os.Stat(markerPath(h)); return os.IsNotExist(err) },
		"stale marker was not cleared")
	require.False(t, s.Ready())
}

func TestRun_ReturnsImmediatelyWhenDisabled(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked on a node with no fabricmanager")
	}
}

// Something else removing the marker must not strand every GPU on the node at
// IN_PROGRESS for the pod's lifetime.
func TestRun_ReassertsAfterExternalRemoval(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	requireEventually(t, func() bool { return s.Ready() }, "marker never published")

	require.NoError(t, os.Remove(markerPath(h)))
	requireEventually(t, func() bool { _, err := os.Stat(markerPath(h)); return err == nil },
		"marker was not re-asserted")
}

func TestDiscard_RemovesMarker(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	require.NoError(t, fabricmanager.WriteReady(h.RootPath(stateDirRel)))

	require.NoError(t, s.Discard(context.Background(), h))
	require.NoFileExists(t, markerPath(h))
	require.False(t, s.Ready())
}

func TestDiscard_NoOpBeforeStage(t *testing.T) {
	require.NoError(t, New(Options{}).Discard(context.Background(), host.New(t.TempDir())))
}

func TestReload_IsNoOp(t *testing.T) {
	require.NoError(t, New(Options{}).Reload(context.Background(), enabledState()))
}

func requireEventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
