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

// The chart sets the state dir only where it also mounts it, so an empty value
// means no fabricmanager on this node.
func TestStage_NoOpWithoutStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))
	require.True(t, s.Ready(), "a node without fabricmanager is ready, not pending")
	require.NoDirExists(t, h.RootPath(stateDirRel))
}

// Stage owns the directory because it is the only method holding a Host, and
// the cdi simulator mounts that path into workloads during the Apply that
// follows.
func TestStage_CreatesStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	require.DirExists(t, h.RootPath(stateDirRel))
	require.False(t, s.Ready(), "readiness waits for the daemon to publish")
}

// Run holds a reference for the agent's lifetime, so a second Stage must not
// swap the daemon out from under it.
func TestStage_KeepsTheSameDaemonAcrossReconciles(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	first := s.daemon.Load()
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	require.Same(t, first, s.daemon.Load())
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

func TestRun_PublishesReadinessThroughTheSimulator(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	require.Eventually(t, s.Ready, 5*time.Second, 10*time.Millisecond,
		"readiness never surfaced through the simulator")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on cancellation")
	}
}

func TestDiscard_WithdrawsReadiness(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(context.Background(), h, enabledState()))
	require.NoError(t, fabricmanager.WriteReady(h.RootPath(stateDirRel)))

	require.NoError(t, s.Discard(context.Background(), h))
	require.NoFileExists(t, fabricmanager.MarkerPath(h.RootPath(stateDirRel)))
	require.False(t, s.Ready())
}

func TestDiscard_NoOpBeforeStage(t *testing.T) {
	require.NoError(t, New(Options{}).Discard(context.Background(), host.New(t.TempDir())))
}

func TestReload_IsNoOp(t *testing.T) {
	require.NoError(t, New(Options{}).Reload(context.Background(), enabledState()))
}
