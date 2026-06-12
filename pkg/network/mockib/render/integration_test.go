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
//	make -C pkg/network/mockib                       # build libibmocksys.so
//	go test -tags=integration ./pkg/network/mockib/render/...
package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "getwd")
	shim := filepath.Join(wd, "..", "libibmocksys.so")
	if _, err := os.Stat(shim); err != nil {
		t.Skipf("shim not built: %v (run `make -C pkg/network/mockib`)", err)
	}

	root := t.TempDir()
	err = Render(Options{
		IB: config.Infiniband{
			Enabled:   true,
			HCAType:   "MT4129",
			FWVersion: "28.39.2048",
			RateGbps:  400,
		},
		GPUCount: 2,
		NodeName: "test-node",
		Output:   root,
	})
	require.NoError(t, err, "Render")

	cmd := exec.Command(ibstat)
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_IB=sysfs",
		"MOCK_IB_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ibstat failed\noutput:\n%s", out)
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
		require.Contains(t, got, s, "ibstat output missing %q\nfull output:\n%s", s, got)
	}

	// Spot-check end-to-end that branches not exercised by `ibstat` are
	// rendered: per-HCA counter files (read by perfquery / iblinkinfo)
	// and the global infiniband_mad/abi_version (read by libibumad init).
	for _, rel := range []string{
		"sys/class/infiniband/mlx5_0/ports/1/counters/port_xmit_data",
		"sys/class/infiniband/mlx5_0/ports/1/counters/port_rcv_data",
		"sys/class/infiniband/mlx5_1/ports/1/counters/port_xmit_packets",
		"sys/class/infiniband_mad/abi_version",
	} {
		_, err := os.Stat(filepath.Join(root, rel))
		require.NoError(t, err, "expected rendered file %s", rel)
	}
}

func TestIbvDevinfo_List_Integration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("integration test requires linux")
	}
	ibv, err := exec.LookPath("ibv_devinfo")
	if err != nil {
		t.Skip("ibv_devinfo not installed (apt-get install rdma-core)")
	}
	wd, err := os.Getwd()
	require.NoError(t, err, "getwd")
	shim := filepath.Join(wd, "..", "libibmocksys.so")
	if _, err := os.Stat(shim); err != nil {
		t.Skipf("shim not built: %v (run `make -C pkg/network/mockib`)", err)
	}

	root := t.TempDir()
	err = Render(Options{
		IB: config.Infiniband{
			Enabled:   true,
			HCAType:   "MT4129",
			FWVersion: "28.39.2048",
			RateGbps:  400,
		},
		GPUCount: 2,
		NodeName: "test-node",
		Output:   root,
	})
	require.NoError(t, err, "Render")

	cmd := exec.Command(ibv, "-l")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_IB=sysfs",
		"MOCK_IB_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ibv_devinfo -l failed\noutput:\n%s", out)
	got := string(out)
	for _, want := range []string{"mlx5_0", "mlx5_1"} {
		require.Contains(t, got, want, "ibv_devinfo -l missing %q\nfull output:\n%s", want, got)
	}
}
