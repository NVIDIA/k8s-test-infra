// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
)

func TestRender_Disabled(t *testing.T) {
	dir := t.TempDir()
	// Populate every Infiniband field except `Enabled` so the test would
	// catch a regression where `Enabled` is ignored and Render() proceeds
	// based on the rest of the struct alone.
	ib := config.Infiniband{
		Enabled:          false,
		HCAType:          "MT4129",
		FWVersion:        "28.39.2048",
		HWRev:            "0",
		BoardID:          "MT_0000000884",
		GUIDPrefix:       "001122334455",
		HCAsPerGPU:       2,
		HCACountOverride: 4,
		RateGbps:         400,
		PortState:        "ACTIVE",
		PhysState:        "LinkUp",
		LinkLayer:        "InfiniBand",
		NodeDescTemplate: "{node_name} mlx5_{idx}",
	}
	if err := Render(Options{IB: ib, Output: dir, GPUCount: 8, NodeName: "host1"}); err != nil {
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
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+strconv.Itoa(i))
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
	mustRead("sys/class/infiniband_verbs/uverbs0/dev", "231:0")

	modaliasPath := filepath.Join(dir, "sys/class/infiniband/mlx5_0/device/modalias")
	modalias, err := os.ReadFile(modaliasPath)
	if err != nil {
		t.Fatalf("read modalias: %v", err)
	}
	// Must be in the exact kernel modalias grammar so libibverbs' fnmatch
	// against provider match tables (e.g. mlx5's "pci:v000015B3d*sv*sd*bc*sc*i*")
	// can claim the device. See render.go for the rationale.
	const wantModalias = "pci:v000015B3d00001017sv000015B3sd00000008bc02sc00i00\n"
	if string(modalias) != wantModalias {
		t.Fatalf("modalias = %q, want %q", modalias, wantModalias)
	}

	// `gid_attrs` must be a directory in real Linux sysfs; libibverbs and
	// iblinkinfo opendir() it. A regular file would yield ENOTDIR.
	gidAttrs := filepath.Join(dir, "sys/class/infiniband/mlx5_0/ports/1/gid_attrs")
	if st, err := os.Stat(gidAttrs); err != nil {
		t.Errorf("missing gid_attrs: %v", err)
	} else if !st.IsDir() {
		t.Errorf("gid_attrs must be a directory, got mode %v", st.Mode())
	}

	// port_guid is exposed by real mlx5 ports and must follow node_guid's
	// formatting. The lower 16 bits encode (nodeID<<8)|idx for node_guid
	// and (nodeID<<8)|(idx+1) for port_guid — matches real Mellanox U/L-bit
	// behavior. For NodeName "host1", nodeID is 0xfa.
	mustRead("sys/class/infiniband/mlx5_0/node_guid", "a088:c203:00ab:fa00")
	mustRead("sys/class/infiniband/mlx5_0/ports/1/port_guid", "a088:c203:00ab:fa01")
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
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+strconv.Itoa(i))
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
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+strconv.Itoa(i))
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

func TestRender_NodeUniqueGUIDsAcrossNodes(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	opts := func(node, out string) Options {
		return Options{IB: ib, GPUCount: 2, NodeName: node, Output: out}
	}
	if err := Render(opts("worker-a", dirA)); err != nil {
		t.Fatal(err)
	}
	if err := Render(opts("worker-b", dirB)); err != nil {
		t.Fatal(err)
	}
	readGUID := func(root, ca string) string {
		b, err := os.ReadFile(filepath.Join(root, "sys/class/infiniband", ca, "node_guid"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	gA := readGUID(dirA, "mlx5_0")
	gB := readGUID(dirB, "mlx5_0")
	if gA == gB {
		t.Fatalf("node_guid collision for same hca idx: %q", gA)
	}
	lidA, _ := os.ReadFile(filepath.Join(dirA, "sys/class/infiniband/mlx5_0/ports/1/lid"))
	lidB, _ := os.ReadFile(filepath.Join(dirB, "sys/class/infiniband/mlx5_0/ports/1/lid"))
	if strings.TrimSpace(string(lidA)) == strings.TrimSpace(string(lidB)) {
		t.Fatalf("lid collision: %q", lidA)
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

