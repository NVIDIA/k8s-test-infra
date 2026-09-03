// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabricmanager

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fabricmanager"
)

func TestMain(m *testing.M) {
	zap.ReplaceGlobals(zap.NewNop())
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
	require.NoDirExists(t, h.HostPath(fabricmanager.DefaultStateDir))
}

// Stage owns the directory because it is the only method holding a Host, and
// the cdi simulator mounts that path into workloads during the Apply that
// follows.
func TestStage_CreatesStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	require.NoError(t, s.Stage(t.Context(), h, enabledState()))
	require.DirExists(t, h.HostPath(fabricmanager.DefaultStateDir))
	require.False(t, s.Ready(), "readiness waits for the daemon to publish")
}

// The chart mounts whatever directory it names into both containers, so Stage
// has to follow that value rather than a path this package picks.
func TestStage_FollowsTheConfiguredStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})

	const stateDir = "/run/nvidia-fabricmanager"
	state := &agent.State{Fabric: agent.FabricState{ManagerStateDir: stateDir}}

	require.NoError(t, s.Stage(t.Context(), h, state))
	require.DirExists(t, h.HostPath(stateDir))
	require.NoDirExists(t, h.HostPath(fabricmanager.DefaultStateDir))
}

// Run is launched once, so a node with no fabricmanager has to stay in the loop
// rather than return, in case a reconcile turns it on.
func TestRun_ParksWhenDisabled(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(t.Context(), h, &agent.State{}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	select {
	case <-done:
		t.Fatal("Run returned instead of parking for a later reconcile")
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on cancellation")
	}
}

// A reconcile that turns fabricmanager off must stop the daemon, or it keeps
// re-asserting the marker every 2s and GPUs go on reporting COMPLETED.
func TestRun_WithdrawsTheMarkerWhenDisabled(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(t.Context(), h, enabledState()))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	marker := fabricmanager.MarkerPath(h.HostPath(fabricmanager.DefaultStateDir))
	require.Eventually(t, func() bool { return fileExists(marker) },
		5*time.Second, 10*time.Millisecond, "marker never appeared")

	require.NoError(t, s.Stage(t.Context(), h, &agent.State{}))
	require.Eventually(t, func() bool { return !fileExists(marker) },
		5*time.Second, 10*time.Millisecond, "marker outlived the disabled daemon")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on cancellation")
	}
}

func TestRun_MovesReadinessToTheChangedStateDir(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(t.Context(), h, enabledState()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	oldMarker := fabricmanager.MarkerPath(h.HostPath(fabricmanager.DefaultStateDir))
	require.Eventually(t, func() bool { return fileExists(oldMarker) },
		5*time.Second, 10*time.Millisecond, "initial marker never appeared")

	const newStateDir = "/run/nvidia-fabricmanager"
	state := &agent.State{Fabric: agent.FabricState{ManagerStateDir: newStateDir}}
	require.NoError(t, s.Stage(t.Context(), h, state))

	newMarker := fabricmanager.MarkerPath(h.HostPath(newStateDir))
	require.Eventually(t, func() bool { return fileExists(newMarker) },
		5*time.Second, 10*time.Millisecond, "marker never appeared in the new directory")
	require.NoFileExists(t, oldMarker)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on cancellation")
	}
}

// The mirror case: a node that starts disabled still gets its daemon served
// when a reconcile enables it, even though Run was already launched.
func TestRun_ServesADaemonEnabledAfterStartup(t *testing.T) {
	h := host.New(t.TempDir())
	s := New(Options{})
	require.NoError(t, s.Stage(t.Context(), h, &agent.State{}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	require.NoError(t, s.Stage(t.Context(), h, enabledState()))
	require.Eventually(t, s.Ready, 5*time.Second, 10*time.Millisecond,
		"readiness never surfaced after a reconcile enabled fabricmanager")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on cancellation")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
	require.NoError(t, fabricmanager.WriteReady(h.HostPath(fabricmanager.DefaultStateDir)))

	require.NoError(t, s.Discard(context.Background(), h))
	require.NoFileExists(t, fabricmanager.MarkerPath(h.HostPath(fabricmanager.DefaultStateDir)))
	require.False(t, s.Ready())
}

func TestDiscard_NoOpBeforeStage(t *testing.T) {
	require.NoError(t, New(Options{}).Discard(context.Background(), host.New(t.TempDir())))
}

func TestReload_IsNoOp(t *testing.T) {
	require.NoError(t, New(Options{}).Reload(context.Background(), enabledState()))
}
