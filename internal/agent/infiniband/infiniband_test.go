// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

// TestMain silences the package's daemon output. The simulator picks up
// slog.Default(), which the agent binary configures via internal/logging.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"off", ModeOff, false},
		{"sysfs", ModeSysfs, false},
		{"full", ModeFull, false},
		{"FULL", ModeFull, false},
		{" Sysfs ", ModeSysfs, false},
		{"", ModeOff, false},
		{"enabled", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseMode(c.in)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.want, got)
		})
	}
}

// Discard must leave gpudriver's files alone: both simulators write into
// driver/usr/bin and driver/usr/lib64.
func TestDiscard_LeavesGPUDriverFilesIntact(t *testing.T) {
	isolateSources(t)
	seedImageSources(t)

	h := newTestHost(t)
	seedGPUDriver := func() {
		for _, rel := range []string{"driver/usr/bin/nvidia-smi", "driver/usr/lib64/libnvidia-ml.so.1"} {
			p := h.RootPath(rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte("gpudriver"), 0o755))
		}
	}
	seedGPUDriver()

	s := New(Options{Mode: ModeSysfs})
	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))
	require.NoError(t, s.Discard(context.Background(), h))

	require.NoDirExists(t, h.RootPath("ib"))
	require.NoFileExists(t, h.RootPath("driver/usr/bin/ibstat"))
	require.NoFileExists(t, h.RootPath("driver/usr/lib64/libibmad.so.5"))
	require.NoFileExists(t, h.RootPath("driver/usr/local/lib/libibmockumad.so.1"))
	require.NoFileExists(t, h.RootPath("driver/usr/bin/check-fabric"))
	require.NoDirExists(t, h.RootPath("driver/etc/libibverbs.d"))

	require.FileExists(t, h.RootPath("driver/usr/bin/nvidia-smi"))
	require.FileExists(t, h.RootPath("driver/usr/lib64/libnvidia-ml.so.1"))
}

func TestDiscard_NoOpBeforeStage(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})
	require.NoError(t, s.Discard(context.Background(), h))
}

func TestRun_ReturnsImmediatelyWhenNotFull(t *testing.T) {
	for _, mode := range []Mode{ModeOff, ModeSysfs} {
		t.Run(string(mode), func(t *testing.T) {
			s := New(Options{Mode: mode})
			done := make(chan error, 1)
			go func() { done <- s.Run(context.Background()) }()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Run did not return for a non-full mode")
			}
		})
	}
}

func TestRun_ServesAndReports(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	socket := filepath.Join(t.TempDir(), "mock-ib.sock")
	s := New(Options{Mode: ModeFull, SocketPath: socket})

	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))
	require.False(t, s.Ready(), "full mode is not ready until the daemon serves")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	requireEventually(t, func() bool { return s.Ready() }, "daemon never became ready")
	requireDialable(t, socket)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}
	require.False(t, s.Ready(), "readiness must drop once the daemon stops")
}

// A profile with IB off must not leave readiness stuck waiting on a daemon that
// is never going to start.
func TestRun_ReadyWithoutDaemonWhenIBDisabled(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeFull, SocketPath: filepath.Join(t.TempDir(), "s.sock")})

	require.NoError(t, s.Stage(context.Background(), h, testState(agent.NetworkShape{})))
	require.True(t, s.Ready())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Run parks instead of returning, so a later profile edit can still start it.
	select {
	case <-done:
		t.Fatal("Run returned instead of waiting for IB to be enabled")
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}
}

func TestReload_OnlyRestartsWhenShapeChanges(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeFull, SocketPath: filepath.Join(t.TempDir(), "s.sock")})
	ctx := context.Background()

	net := testNetwork()
	require.NoError(t, s.Stage(ctx, h, testState(net)))

	// Same shape re-staged: nothing to restart.
	require.NoError(t, s.Stage(ctx, h, testState(net)))
	require.NoError(t, s.Reload(ctx, testState(net)))
	require.Empty(t, s.restart, "an unchanged shape must not request a restart")

	// A real change does.
	changed := net
	changed.HCACount = 4
	require.NoError(t, s.Stage(ctx, h, testState(changed)))
	require.NoError(t, s.Reload(ctx, testState(changed)))
	require.Len(t, s.restart, 1, "a changed shape must request a restart")

	// The request is consumed once; a repeat reconcile is quiet again.
	<-s.restart
	require.NoError(t, s.Stage(ctx, h, testState(changed)))
	require.NoError(t, s.Reload(ctx, testState(changed)))
	require.Empty(t, s.restart)
}

func TestReload_RerendersTreeForNewShape(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})
	ctx := context.Background()

	require.NoError(t, s.Stage(ctx, h, testState(testNetwork())))
	require.NoDirExists(t, h.RootPath("ib/sys/class/infiniband/mlx5_3"))

	grown := testNetwork()
	grown.HCACount = 4
	require.NoError(t, s.Stage(ctx, h, testState(grown)))
	require.DirExists(t, h.RootPath("ib/sys/class/infiniband/mlx5_3"))
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

func requireDialable(t *testing.T, socket string) {
	t.Helper()
	c, err := net.Dial("unix", socket)
	require.NoError(t, err)
	require.NoError(t, c.Close())
}

// The socket must land on the host mount. The agent container mounts the
// hostPath only at /host/var/lib/nvml-mock, so a container-absolute default
// would create the socket inside the container where no workload can reach it.
func TestStage_DerivesSocketPathUnderHostRoot(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeFull})

	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))

	got := s.socketPath.Load()
	require.NotNil(t, got)
	require.Equal(t, h.RootPath("run", "mock-ib.sock"), *got)
}

func TestStage_SocketPathOverrideWins(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	want := filepath.Join(t.TempDir(), "custom.sock")
	s := New(Options{Mode: ModeFull, SocketPath: want})

	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))
	require.Equal(t, want, *s.socketPath.Load())
}
