// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Integration test: the real ibping binary against a rendered tree, both shims,
// and an in-process UMAD server, pinging a local port GUID.
//
// Run with:
//
//	make -C shims/libibmock
//	go test -tags=integration ./internal/ib/sysfs/ -run TestIbping -v
package sysfs_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/daemon"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
)

func TestIbping_Loopback_Integration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("integration test requires linux")
	}
	ibping, err := exec.LookPath("ibping")
	if err != nil {
		t.Skip("ibping not installed (apt-get install infiniband-diags)")
	}

	wd, err := os.Getwd()
	require.NoError(t, err, "getwd")
	shimDir := filepath.Join(wd, "..", "..", "..", "shims", "libibmock")
	shimSys := filepath.Join(shimDir, "libibmocksys.so")
	shimUmad := filepath.Join(shimDir, "libibmockumad.so")
	for _, p := range []string{shimSys, shimUmad} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("shim not built: %v (run `make -C shims/libibmock`)", err)
		}
	}

	root := t.TempDir()
	nodeName := "host1"
	err = sysfs.Render(sysfs.Options{
		IB: config.Infiniband{
			Enabled:   true,
			HCAType:   "MT4129",
			FWVersion: "28.39.2048",
			RateGbps:  400,
		},
		GPUCount: 2,
		NodeName: nodeName,
		RootDir:  root,
	})
	require.NoError(t, err)

	lidPath := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/lid")
	guidPath := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/port_guid")
	lidBytes, err := os.ReadFile(lidPath)
	require.NoError(t, err, "read lid")
	lid := strings.TrimSpace(string(lidBytes))
	require.NotEmpty(t, lid, "empty lid")
	guidBytes, err := os.ReadFile(guidPath)
	require.NoError(t, err, "read port_guid")
	guidHex := "0x" + strings.NewReplacer(":", "").Replace(strings.TrimSpace(string(guidBytes)))

	runDir := t.TempDir()
	socketPath := filepath.Join(runDir, "mock-ib.sock")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := daemon.NewServer(daemon.Config{SocketPath: socketPath, IBRoot: root})
	require.NoError(t, err, "new server")
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.ListenAndServe(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-served
	})
	waitForUnixSocket(t, socketPath)

	preload := shimUmad + ":" + shimSys
	runIbping := func(args ...string) {
		t.Helper()
		cmd := exec.Command(ibping, args...)
		cmd.Env = append(os.Environ(),
			"MOCK_IB=full",
			"LD_PRELOAD="+preload,
			"MOCK_IB_ROOT="+root,
			"MOCK_IB_PING_SOCKET="+socketPath,
		)
		out, err := cmd.CombinedOutput()
		got := string(out)

		failPatterns := []string{
			"client_register for mgmt 3 failed",
			"iberror:",
			"can't open UMAD port",
			"can't resolve destination port",
		}
		for _, p := range failPatterns {
			require.NotContains(t, got, p, "ibping %v output contains %q\nerr=%v\noutput:\n%s", args, p, err, got)
		}

		require.NoError(t, err, "ibping %v failed\noutput:\n%s", args, got)
		require.NotContains(t, got, ", 0 received", "ibping %v reported no replies\noutput:\n%s", args, got)
		require.NotContains(t, got, "100% packet loss", "ibping %v reported no replies\noutput:\n%s", args, got)
		require.Contains(t, got, "packets transmitted", "ibping %v missing statistics\noutput:\n%s", args, got)
		require.True(t,
			regexp.MustCompile(`[0-9]+ packets transmitted, [1-9][0-9]* received`).MatchString(got) ||
				strings.Contains(got, "0% packet loss"),
			"ibping %v did not report successful replies\noutput:\n%s", args, got)
	}

	runIbping("-c", "1", lid)
	runIbping("-G", "-c", "1", guidHex)
}

func waitForUnixSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Failf(t, "unix socket not ready", "unix socket %s not ready", path)
}
