// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// testNetwork is a fully-resolved shape, as FileSource.compileNetwork produces.
func testNetwork() agent.NetworkShape {
	return agent.NetworkShape{
		IBEnabled:        true,
		HCACount:         2,
		HCAType:          "MT4129",
		FWVersion:        "28.40.1000",
		HWRev:            "0x0",
		BoardID:          "MT_0000000838",
		NodeDescTemplate: "{node_name} mlx5_{idx}",
		LinkLayer:        "InfiniBand",
		RateGbps:         400,
		PortState:        "ACTIVE",
		PhysState:        "LinkUp",
		GUIDPrefix:       "9b88c2:0300:ab",
	}
}

func testState(net agent.NetworkShape) *agent.State {
	return &agent.State{
		Node:      agent.NodeMeta{NodeName: "node-0"},
		NodeShape: agent.NodeShape{NumGPUs: 2, Network: net},
	}
}

func newTestHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

// isolateSources points the image-path package vars at empty temp dirs so a
// test never depends on what the machine running it happens to have installed.
func isolateSources(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	t.Cleanup(func(orig ...string) func() {
		return func() {
			toolBundleRoot, shimGlob, verbsConfDir, checkFabric = orig[0], orig[1], orig[2], orig[3]
		}
	}(toolBundleRoot, shimGlob, verbsConfDir, checkFabric))

	toolBundleRoot = filepath.Join(empty, "nvml-mock-ib")
	shimGlob = filepath.Join(empty, "lib", "libibmock*.so*")
	verbsConfDir = filepath.Join(empty, "libibverbs.d")
	checkFabric = filepath.Join(empty, "check-fabric")
}

func TestStage_RendersSysfsTree(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})

	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))
	require.True(t, s.Ready())

	ca := h.RootPath("ib/sys/class/infiniband/mlx5_0")
	for _, attr := range []string{"node_type", "fw_ver", "board_id", "node_desc", "hw_rev"} {
		require.FileExists(t, filepath.Join(ca, attr))
	}
	for _, attr := range []string{"state", "phys_state", "rate", "lid"} {
		require.FileExists(t, filepath.Join(ca, "ports/1", attr))
	}
	for _, dir := range []string{"gids", "pkeys", "counters", "gid_attrs"} {
		require.DirExists(t, filepath.Join(ca, "ports/1", dir))
	}

	// HCACount drives how many CAs appear, independent of GPU count.
	require.DirExists(t, h.RootPath("ib/sys/class/infiniband/mlx5_1"))
	require.NoDirExists(t, h.RootPath("ib/sys/class/infiniband/mlx5_2"))
}

func TestStage_ProfileValuesReachSysfs(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})

	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))

	ca := h.RootPath("ib/sys/class/infiniband/mlx5_0")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(ca, rel))
		require.NoError(t, err)
		return string(b)
	}
	require.Contains(t, read("fw_ver"), "28.40.1000")
	require.Contains(t, read("board_id"), "MT_0000000838")
	require.Contains(t, read("node_desc"), "node-0")
	require.Contains(t, read("ports/1/state"), "ACTIVE")
	require.Contains(t, read("ports/1/phys_state"), "LinkUp")
}

func TestStage_IsIdempotent(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})
	state := testState(testNetwork())

	require.NoError(t, s.Stage(context.Background(), h, state))
	first := snapshotTree(t, h.RootPath("ib"))

	require.NoError(t, s.Stage(context.Background(), h, state))
	require.Equal(t, first, snapshotTree(t, h.RootPath("ib")))
}

func TestStage_DisabledTierStagesShimsOnly(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
		net  agent.NetworkShape
	}{
		{"mode off", ModeOff, testNetwork()},
		{"ib disabled in profile", ModeFull, agent.NetworkShape{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateSources(t)
			seedImageSources(t)
			h := newTestHost(t)
			s := New(Options{Mode: c.mode})

			require.NoError(t, s.Stage(context.Background(), h, testState(c.net)))
			require.True(t, s.Ready(), "a no-op stage is still ready")

			// The root is created but left empty, masking any real host IB.
			require.DirExists(t, h.RootPath("ib"))
			require.NoDirExists(t, h.RootPath("ib/sys/class/infiniband"))

			// The NRI plugin preloads the shims whatever the tier, so they have
			// to resolve here too. The tools they serve stay behind.
			require.FileExists(t, h.RootPath("driver/usr/local/lib/libibmockumad.so.1"))
			require.NoFileExists(t, h.RootPath("driver/usr/bin/ibstat"))
		})
	}
}

func TestStage_CopiesToolsShimsAndConfig(t *testing.T) {
	isolateSources(t)
	seedImageSources(t)

	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})
	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))

	require.FileExists(t, h.RootPath("driver/usr/bin/ibstat"))
	require.FileExists(t, h.RootPath("driver/usr/lib64/libibmad.so.5"))
	require.FileExists(t, h.RootPath("driver/usr/local/lib/libibmockumad.so.1"))
	require.FileExists(t, h.RootPath("driver/etc/libibverbs.d/mlx5.driver"))
	require.FileExists(t, h.RootPath("driver/usr/bin/check-fabric"))
}

// Absent image sources are not an error: images are built without the IB stack.
func TestStage_ToleratesMissingImageSources(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	s := New(Options{Mode: ModeSysfs})

	require.NoError(t, s.Stage(context.Background(), h, testState(testNetwork())))
	require.True(t, s.Ready())
	require.NoFileExists(t, h.RootPath("driver/usr/bin/check-fabric"))
}

// seedImageSources fabricates the container-image layout bundle-ib-tools.sh
// produces, so the copy paths are exercised without a real IB install.
func seedImageSources(t *testing.T) {
	t.Helper()
	write := func(path string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("elf"), 0o755))
	}
	write(filepath.Join(toolBundleRoot, "bin", "ibstat"))
	write(filepath.Join(toolBundleRoot, "bin", "iblinkinfo"))
	write(filepath.Join(toolBundleRoot, "lib64", "libibmad.so.5"))
	write(filepath.Join(filepath.Dir(shimGlob), "libibmockumad.so.1"))
	write(filepath.Join(verbsConfDir, "mlx5.driver"))
	write(checkFabric)
}

// snapshotTree maps every file under root to its contents, so an idempotency
// check compares bytes rather than mtimes.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(b)
		return nil
	}))
	return out
}
