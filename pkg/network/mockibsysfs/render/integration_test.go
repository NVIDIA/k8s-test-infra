// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Integration test that renders a fake sysfs tree and runs the real ibstat
// binary against it via libibmocksys.so. Skipped unless the integration tag
// is set AND we are on a Linux host with infiniband-diags + the prebuilt
// shim available.
//
// Run with:
//
//	make -C pkg/network/mockibsysfs                       # build libibmocksys.so
//	go test -tags=integration ./pkg/network/mockibsysfs/render/...
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

func TestIbstat_Integration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("integration test requires linux")
	}
	ibstat, err := exec.LookPath("ibstat")
	if err != nil {
		t.Skip("ibstat not installed (apt-get install infiniband-diags)")
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

	cmd := exec.Command(ibstat)
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_IB_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ibstat failed: %v\noutput:\n%s", err, out)
	}
	want := []string{
		"CA 'mlx5_0'",
		"CA 'mlx5_1'",
		"CA type: MT4129",
		"Firmware version: 28.39.2048",
		"State: Active",
		"Physical state: LinkUp",
		"Rate: 400",
		"Link layer: InfiniBand",
	}
	got := string(out)
	for _, s := range want {
		if !strings.Contains(got, s) {
			t.Errorf("ibstat output missing %q\nfull output:\n%s", s, got)
		}
	}
}
