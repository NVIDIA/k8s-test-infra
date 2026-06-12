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
	"github.com/stretchr/testify/require"
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
	err := Render(Options{IB: ib, Output: dir, GPUCount: 8, NodeName: "host1"})
	require.NoError(t, err, "Render")
	entries, _ := os.ReadDir(dir)
	require.Len(t, entries, 0, "expected no output when disabled")
}

func TestRender_DefaultsAndCount(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true},
		GPUCount: 4,
		NodeName: "host1",
		Output:   dir,
	})
	require.NoError(t, err, "Render")
	for i := 0; i < 4; i++ {
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+strconv.Itoa(i))
		_, err := os.Stat(caDir)
		require.NoError(t, err, "missing CA dir mlx5_%d", i)
	}
	_, err = os.Stat(filepath.Join(dir, "sys/class/infiniband", "mlx5_4"))
	require.True(t, os.IsNotExist(err), "unexpected mlx5_4 (gpu_count=4 hcas_per_gpu=1): %v", err)

	mustRead := func(rel, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(dir, rel))
		require.NoError(t, err, "read %s", rel)
		require.Equal(t, strings.TrimSpace(want), strings.TrimSpace(string(got)), "%s", rel)
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
	require.NoError(t, err, "read modalias")
	// Must be in the exact kernel modalias grammar so libibverbs' fnmatch
	// against provider match tables (e.g. mlx5's "pci:v000015B3d*sv*sd*bc*sc*i*")
	// can claim the device. See render.go for the rationale.
	const wantModalias = "pci:v000015B3d00001017sv000015B3sd00000008bc02sc00i00\n"
	require.Equal(t, wantModalias, string(modalias), "modalias")

	// `gid_attrs` must be a directory in real Linux sysfs; libibverbs and
	// iblinkinfo opendir() it. A regular file would yield ENOTDIR.
	gidAttrs := filepath.Join(dir, "sys/class/infiniband/mlx5_0/ports/1/gid_attrs")
	st, err := os.Stat(gidAttrs)
	require.NoError(t, err, "missing gid_attrs")
	require.True(t, st.IsDir(), "gid_attrs must be a directory, got mode %v", st.Mode())

	// port_guid is exposed by real mlx5 ports and must follow node_guid's
	// formatting. The lower 32 bits pack the node id (bits 9..31) and HCA
	// index (bits 1..8); bit 0 is the EUI-64 U/L bit, set on the port GUID
	// and clear on the node GUID — matching real Mellanox behavior and what
	// fabric.nodeGUIDFromPortGUID inverts. For NodeName "host1", nodeID is
	// 0x6bde52fa, so the node GUID lower dword is 0xbca5f400 and the port
	// GUID is 0xbca5f401.
	mustRead("sys/class/infiniband/mlx5_0/node_guid", "a088:c203:bca5:f400")
	mustRead("sys/class/infiniband/mlx5_0/ports/1/port_guid", "a088:c203:bca5:f401")
}

func TestRender_HCAsPerGPU(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCAsPerGPU: 2},
		GPUCount: 4,
		Output:   dir,
	})
	require.NoError(t, err, "Render")
	for i := 0; i < 8; i++ {
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+strconv.Itoa(i))
		_, err := os.Stat(caDir)
		require.NoError(t, err, "missing CA dir mlx5_%d", i)
	}
}

func TestRender_HCACountOverride(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: 2},
		GPUCount: 16, // ignored
		Output:   dir,
	})
	require.NoError(t, err, "Render")
	for i := 0; i < 2; i++ {
		caDir := filepath.Join(dir, "sys/class/infiniband", "mlx5_"+strconv.Itoa(i))
		_, err := os.Stat(caDir)
		require.NoError(t, err, "missing CA dir mlx5_%d", i)
	}
	_, err = os.Stat(filepath.Join(dir, "sys/class/infiniband", "mlx5_2"))
	require.True(t, os.IsNotExist(err), "override should cap count: mlx5_2 exists: %v", err)
}

func TestRender_RateMapping(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{100, "100 Gb/sec (4X EDR)"},
		{200, "200 Gb/sec (4X HDR)"},
		{400, "400 Gb/sec (4X NDR)"},
		{800, "800 Gb/sec (4X XDR)"},
		{50, "50 Gb/sec (4X)"},
	}
	for _, tc := range cases {
		got := formatRate(tc.in)
		require.Equal(t, tc.want, got, "formatRate(%d)", tc.in)
	}
}

func TestRender_GUIDPrefixNormalization(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, GUIDPrefix: "AA:BB:CC:00:11:22"},
		GPUCount: 1,
		Output:   dir,
	})
	require.NoError(t, err, "Render")
	got, _ := os.ReadFile(filepath.Join(dir, "sys/class/infiniband/mlx5_0/node_guid"))
	want := "aabb:cc00:0000:0000\n"
	require.Equal(t, want, string(got), "node_guid")
}

func TestRender_NodeUniqueGUIDsAcrossNodes(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	opts := func(node, out string) Options {
		return Options{IB: ib, GPUCount: 2, NodeName: node, Output: out}
	}
	err := Render(opts("worker-a", dirA))
	require.NoError(t, err)
	err = Render(opts("worker-b", dirB))
	require.NoError(t, err)
	readGUID := func(root, ca string) string {
		b, err := os.ReadFile(filepath.Join(root, "sys/class/infiniband", ca, "node_guid"))
		require.NoError(t, err)
		return strings.TrimSpace(string(b))
	}
	gA := readGUID(dirA, "mlx5_0")
	gB := readGUID(dirB, "mlx5_0")
	require.NotEqual(t, gB, gA, "node_guid collision for same hca idx")
	lidA, _ := os.ReadFile(filepath.Join(dirA, "sys/class/infiniband/mlx5_0/ports/1/lid"))
	lidB, _ := os.ReadFile(filepath.Join(dirB, "sys/class/infiniband/mlx5_0/ports/1/lid"))
	require.NotEqual(t, strings.TrimSpace(string(lidB)), strings.TrimSpace(string(lidA)), "lid collision")
}

func TestRender_NodePortGUIDsAvoidKnownHashLowBitCollision(t *testing.T) {
	// "38" and "worker-44" have the same low 11 bits in FNV-1a. The renderer
	// must not key GUID/LID identity only on those low bits, or cross-node
	// fabric routing by rendered sysfs identity becomes ambiguous.
	dirA := t.TempDir()
	dirB := t.TempDir()
	ib := config.Infiniband{Enabled: true, HCACountOverride: 2}
	err := Render(Options{IB: ib, NodeName: "38", Output: dirA})
	require.NoError(t, err)
	err = Render(Options{IB: ib, NodeName: "worker-44", Output: dirB})
	require.NoError(t, err)
	read := func(root, rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err, "read %s", rel)
		return strings.TrimSpace(string(b))
	}
	for _, rel := range []string{
		"sys/class/infiniband/mlx5_0/node_guid",
		"sys/class/infiniband/mlx5_0/ports/1/port_guid",
		"sys/class/infiniband/mlx5_0/ports/1/lid",
	} {
		a, b := read(dirA, rel), read(dirB, rel)
		require.NotEqual(t, b, a, "%s collision for known node-name pair", rel)
	}
}

func TestRender_NodePortGUIDsNoOverlap(t *testing.T) {
	// Regression: the previous encoding packed the HCA index into the GUID's
	// lowest bits as node=base|idx, port=base|(idx+1), so port_guid(mlx5_i)
	// collided with node_guid(mlx5_{i+1}). Reserving bit 0 as the U/L bit and
	// putting the index in bits 1..4 keeps every node/port GUID distinct.
	dir := t.TempDir()
	const hcas = 20
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: hcas},
		NodeName: "worker-a",
		Output:   dir,
	})
	require.NoError(t, err, "Render")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		require.NoError(t, err, "read %s", rel)
		return strings.TrimSpace(string(b))
	}
	seen := map[string]string{}
	for i := 0; i < hcas; i++ {
		ca := "mlx5_" + strconv.Itoa(i)
		for _, kind := range []struct{ rel, what string }{
			{filepath.Join("sys/class/infiniband", ca, "node_guid"), ca + " node_guid"},
			{filepath.Join("sys/class/infiniband", ca, "ports/1/port_guid"), ca + " port_guid"},
		} {
			v := read(kind.rel)
			prev, dup := seen[v]
			require.False(t, dup, "GUID collision: %s and %s both == %q", prev, kind.what, v)
			seen[v] = kind.what
		}
	}
}

func TestRender_BadGUIDPrefix(t *testing.T) {
	dir := t.TempDir()
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, GUIDPrefix: "tooshort"},
		GPUCount: 1,
		Output:   dir,
	})
	require.Error(t, err, "expected error for bad guid_prefix")
}
