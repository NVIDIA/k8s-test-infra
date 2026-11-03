// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/nri/pkg/adaptation"
	"github.com/stretchr/testify/require"
)

// runtimeSocketEnv names the socket on which a re-executed test binary serves
// the runtime's side of NRI; see TestHelperRuntime.
const runtimeSocketEnv = "MOKKA_TEST_NRI_RUNTIME_SOCKET"

// TestHelperRuntime is not a test. Run in a child process, it serves the
// runtime's side of NRI on the socket runtimeSocketEnv names, until it is
// killed. A runtime in this process could not stand in for a containerd that
// restarts: stopping it leaves the connections of plugins it did not launch
// open, where the kernel closes them when containerd exits.
func TestHelperRuntime(t *testing.T) {
	socket := os.Getenv(runtimeSocketEnv)
	if socket == "" {
		t.Skip("serves the runtime for TestRunRegistersAgainWhenTheRuntimeRestarts")
	}

	syncFn := func(ctx context.Context, cb adaptation.SyncCB) error {
		_, err := cb(ctx, nil, nil)
		return err
	}
	updateFn := func(context.Context, []*adaptation.ContainerUpdate) ([]*adaptation.ContainerUpdate, error) {
		return nil, nil
	}
	dir := filepath.Dir(socket)
	runtime, err := adaptation.New("containerd", "v2.2.0", syncFn, updateFn,
		adaptation.WithSocketPath(socket),
		adaptation.WithPluginPath(filepath.Join(dir, "plugins")),
		adaptation.WithPluginConfigPath(filepath.Join(dir, "conf.d")),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Start())
	select {}
}

// startRuntime runs the runtime's side of NRI on socket in a child process,
// as containerd does, and returns once it accepts connections.
func startRuntime(t *testing.T, socket string) *exec.Cmd {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRuntime$")
	cmd.Env = append(os.Environ(), runtimeSocketEnv+"="+socket)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { killRuntime(cmd) })

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 10*time.Millisecond, "runtime never listened on %s", socket)
	return cmd
}

// killRuntime ends the runtime as a containerd restart does.
func killRuntime(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// containerd drops every plugin's connection when it restarts, as it does
// whenever the GPU Operator's toolkit starts or stops. Catches a Run that
// returns then: the process exits, and the kubelet's back-off on restarting
// it, which grows with every restart, is a window in which new containers come
// up unmocked.
func TestRunRegistersAgainWhenTheRuntimeRestarts(t *testing.T) {
	t.Parallel()

	// A socket path has to fit in sun_path, which a test's own temporary
	// directory can overflow on macOS.
	dir, err := os.MkdirTemp("", "nri")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for _, sub := range []string{"plugins", "conf.d"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, sub), 0o755))
	}
	socket := filepath.Join(dir, "nri.sock")

	runtime := startRuntime(t, socket)

	cfg := DefaultConfig()
	cfg.SocketPath = socket
	plugin := NewPlugin(cfg)
	plugin.registerRetry = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- plugin.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("Run did not return once its context was cancelled")
		}
	})

	registered := func() bool { return plugin.health.readiness().OK }
	require.Eventually(t, registered, 10*time.Second, 10*time.Millisecond, "never registered")

	killRuntime(runtime)
	require.Eventually(t, func() bool { return !registered() }, 5*time.Second, 10*time.Millisecond,
		"still registered after the runtime exited")

	startRuntime(t, socket)
	require.Eventually(t, registered, 10*time.Second, 10*time.Millisecond,
		"did not register with the restarted runtime")

	select {
	case err := <-done:
		t.Fatalf("Run returned while its context was live: %v", err)
	default:
	}
}
