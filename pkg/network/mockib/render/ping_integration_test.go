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
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	mockibDir := filepath.Join(wd, "..")
	shimSys := filepath.Join(mockibDir, "libibmocksys.so")
	shimUmad := filepath.Join(mockibDir, "libibmockumad.so")
	for _, p := range []string{shimSys, shimUmad} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("shim not built: %v (run `make -C pkg/network/mockib`)", err)
		}
	}

	if out, err := exec.Command("make", "-C", mockibDir).CombinedOutput(); err != nil {
		t.Fatalf("make shims: %v\n%s", err, out)
	}

	repoRoot := filepath.Join(wd, "..", "..", "..", "..")
	daemonBin := filepath.Join(t.TempDir(), "mock-ib")
	build := exec.Command("go", "build", "-mod=vendor", "-o", daemonBin, "./cmd/mock-ib")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mock-ib: %v\n%s", err, out)
	}

	root := t.TempDir()
	nodeName := "host1"
	if err := Render(Options{
		IB: config.Infiniband{
			Enabled:   true,
			HCAType:   "MT4129",
			FWVersion: "28.39.2048",
			RateGbps:  400,
		},
		GPUCount: 2,
		NodeName: nodeName,
		Output:   root,
	}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	lidPath := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/lid")
	guidPath := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1/port_guid")
	lidBytes, err := os.ReadFile(lidPath)
	if err != nil {
		t.Fatalf("read lid: %v", err)
	}
	lid := strings.TrimSpace(string(lidBytes))
	if lid == "" {
		t.Fatal("empty lid")
	}
	guidBytes, err := os.ReadFile(guidPath)
	if err != nil {
		t.Fatalf("read port_guid: %v", err)
	}
	guidHex := "0x" + strings.NewReplacer(":", "").Replace(strings.TrimSpace(string(guidBytes)))

	runDir := t.TempDir()
	socketPath := filepath.Join(runDir, "mock-ib.sock")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	daemon := exec.CommandContext(ctx, daemonBin, "-socket", socketPath, "-ib-root", root)
	daemon.Stdout = os.Stderr
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatalf("start mock-ib: %v", err)
	}
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
			"MOCK_IB=1",
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
			if strings.Contains(got, p) {
				t.Fatalf("ibping %v output contains %q\nerr=%v\noutput:\n%s", args, p, err, got)
			}
		}

		if err != nil {
			t.Fatalf("ibping %v failed: %v\noutput:\n%s", args, err, got)
		}
		if strings.Contains(got, ", 0 received") || strings.Contains(got, "100% packet loss") {
			t.Fatalf("ibping %v reported no replies\noutput:\n%s", args, got)
		}
		if !strings.Contains(got, "packets transmitted") {
			t.Fatalf("ibping %v missing statistics\noutput:\n%s", args, got)
		}
		if !regexp.MustCompile(`[0-9]+ packets transmitted, [1-9][0-9]* received`).MatchString(got) &&
			!strings.Contains(got, "0% packet loss") {
			t.Fatalf("ibping %v did not report successful replies\noutput:\n%s", args, got)
		}
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
	t.Fatalf("unix socket %s not ready", path)
}
