// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockibsysfs/config"
)

func TestRender_Disabled(t *testing.T) {
	dir := t.TempDir()
	if err := Render(Options{IB: config.Infiniband{Enabled: false}, Output: dir, GPUCount: 8}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no output when disabled, got %d entries", len(entries))
	}
}

func TestRender_DefaultsAndCount(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true},
		GPUCount: 4,
		NodeName: "host1",
		Output:   dir,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i := 0; i < 4; i++ {
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+itoa(i))
		if _, err := os.Stat(caDir); err != nil {
			t.Errorf("missing CA dir mlx5_%d: %v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sys/class/infiniband", "mlx5_4")); !os.IsNotExist(err) {
		t.Errorf("unexpected mlx5_4 (gpu_count=4 hcas_per_gpu=1): %v", err)
	}

	mustRead := func(rel, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
			t.Errorf("%s: got %q want %q", rel, string(got), want)
		}
	}

	mustRead("sys/class/infiniband/mlx5_0/hca_type", "MT4129")
	mustRead("sys/class/infiniband/mlx5_0/fw_ver", "28.39.2048")
	mustRead("sys/class/infiniband/mlx5_0/node_desc", "host1 mlx5_0")
	mustRead("sys/class/infiniband/mlx5_0/ports/1/state", "4: ACTIVE")
	mustRead("sys/class/infiniband/mlx5_0/ports/1/phys_state", "5: LinkUp")
	mustRead("sys/class/infiniband/mlx5_0/ports/1/rate", "400 Gb/sec (4X NDR)")
	mustRead("sys/class/infiniband/mlx5_0/ports/1/link_layer", "InfiniBand")
	mustRead("sys/class/infiniband_mad/abi_version", "5")
	mustRead("sys/class/infiniband_mad/umad0/ibdev", "mlx5_0")
}

func TestRender_HCAsPerGPU(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCAsPerGPU: 2},
		GPUCount: 4,
		Output:   dir,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i := 0; i < 8; i++ {
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+itoa(i))
		if _, err := os.Stat(caDir); err != nil {
			t.Errorf("missing CA dir mlx5_%d: %v", i, err)
		}
	}
}

func TestRender_HCACountOverride(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: 2},
		GPUCount: 16, // ignored
		Output:   dir,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i := 0; i < 2; i++ {
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+itoa(i))
		if _, err := os.Stat(caDir); err != nil {
			t.Errorf("missing CA dir mlx5_%d: %v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sys/class/infiniband", "mlx5_2")); !os.IsNotExist(err) {
		t.Errorf("override should cap count: mlx5_2 exists: %v", err)
	}
}

func TestRender_RateMapping(t *testing.T) {
	cases := []struct{ in int; want string }{
		{100, "100 Gb/sec (4X EDR)"},
		{200, "200 Gb/sec (4X HDR)"},
		{400, "400 Gb/sec (4X NDR)"},
		{800, "800 Gb/sec (4X XDR)"},
		{50, "50 Gb/sec (4X)"},
	}
	for _, tc := range cases {
		if got := formatRate(tc.in); got != tc.want {
			t.Errorf("formatRate(%d): got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestRender_GUIDPrefixNormalization(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, GUIDPrefix: "AA:BB:CC:00:11:22"},
		GPUCount: 1,
		Output:   dir,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "sys/class/infiniband/mlx5_0/node_guid"))
	want := "aabb:cc00:1122:0000\n"
	if string(got) != want {
		t.Errorf("node_guid: got %q want %q", string(got), want)
	}
}

func TestRender_BadGUIDPrefix(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, GUIDPrefix: "tooshort"},
		GPUCount: 1,
		Output:   dir,
	})
	if err == nil {
		t.Fatalf("expected error for bad guid_prefix, got nil")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
