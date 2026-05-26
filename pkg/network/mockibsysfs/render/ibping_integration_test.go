// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockibsysfs/config"
)

func TestIbping_Integration(t *testing.T) {
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
	shim := filepath.Join(wd, "..", "libibmocksys.so")
	if _, err := os.Stat(shim); err != nil {
		t.Skipf("shim not built: %v (run `make -C pkg/network/mockibsysfs`)", err)
	}

	root := t.TempDir()
	if err := Render(Options{
		IB: config.Infiniband{
			Enabled:   true,
			HCAType:   "MT4129",
			FWVersion: "28.39.2048",
			RateGbps:  400,
		},
		GPUCount: 2,
		NodeName: "test-node",
		Output:   root,
	}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	env := append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_IB_ROOT="+root,
	)

	cmd := exec.Command(ibping, "-c", "1", "-C", "mlx5_0", "-P", "1", "1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ibping self-ping failed: %v\noutput:\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "Pong from") {
		t.Fatalf("ibping output missing Pong from:\n%s", got)
	}
	if !strings.Contains(got, "1 packets transmitted, 1 received") {
		t.Fatalf("ibping did not report 1/1 packets:\n%s", got)
	}
}
