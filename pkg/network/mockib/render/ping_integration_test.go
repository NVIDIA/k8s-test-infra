// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Integration test: rendered sysfs + libibmocksys + libibmockumad + mock-ib
// and the real ibping binary (phase 1 loopback to local port GUID).
//
// Run with:
//
//	make -C pkg/network/mockib
//	go build -mod=vendor -o /tmp/mock-ib ./cmd/mock-ib
//	go test -tags=integration ./pkg/network/mockib/render/ -run TestIbping -v
package render

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

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/stretchr/testify/require"
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
	mockibDir := filepath.Join(wd, "..")
	shimSys := filepath.Join(mockibDir, "libibmocksys.so")
	shimUmad := filepath.Join(mockibDir, "libibmockumad.so")
	for _, p := range []string{shimSys, shimUmad} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("shim not built: %v (run `make -C pkg/network/mockib`)", err)
		}
	}

	out, err := exec.Command("make", "-C", mockibDir).CombinedOutput()
	require.NoError(t, err, "make shims\n%s", out)

	repoRoot := filepath.Join(wd, "..", "..", "..", "..")
	daemonBin := filepath.Join(t.TempDir(), "mock-ib")
	build := exec.Command("go", "build", "-mod=vendor", "-o", daemonBin, "./cmd/mock-ib")
	build.Dir = repoRoot
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "build mock-ib\n%s", buildOut)

	root := t.TempDir()
	nodeName := "host1"
	err = Render(Options{
		IB: config.Infiniband{
			Enabled:   true,
			HCAType:   "MT4129",
			FWVersion: "28.39.2048",
			RateGbps:  400,
		},
		GPUCount: 2,
		NodeName: nodeName,
		Output:   root,
	})
	require.NoError(t, err, "Render")

	lidPath := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/lid")
	guidPath := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/port_guid")
	lidBytes, err := os.ReadFile(lidPath)
	require.NoError(t, err, "read lid")
	lid := strings.TrimSpace(string(lidBytes))
	require.NotEqual(t, "", lid, "empty lid")
	guidBytes, err := os.ReadFile(guidPath)
	require.NoError(t, err, "read port_guid")
	guidHex := "0x" + strings.NewReplacer(":", "").Replace(strings.TrimSpace(string(guidBytes)))

	runDir := t.TempDir()
	socketPath := filepath.Join(runDir, "mock-ib.sock")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	daemon := exec.CommandContext(ctx, daemonBin, "-socket", socketPath, "-ib-root", root)
	daemon.Stdout = os.Stderr
	daemon.Stderr = os.Stderr
	err = daemon.Start()
	require.NoError(t, err, "start mock-ib")
	t.Cleanup(func() {
		cancel()
		_ = daemon.Wait()
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
