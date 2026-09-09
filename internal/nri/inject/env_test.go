// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireNoEnvKey(t *testing.T, env []string, key string) {
	t.Helper()
	for _, item := range env {
		if name, _, ok := strings.Cut(item, "="); ok && name == key {
			require.Failf(t, "unexpected env key", "expected env not to contain key %q, got %q", key, item)
		}
	}
}

// stageTopology writes a topology document into a fresh overlay root and
// returns the root, which is what gates topology injection.
func stageTopology(t *testing.T) string {
	t.Helper()
	overlayHost := t.TempDir()
	topoDir := filepath.Join(overlayHost, "topology")
	require.NoError(t, os.MkdirAll(topoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))
	return overlayHost
}

func TestAdjustPointsTheLoaderAtTheOverlay(t *testing.T) {
	t.Parallel()

	container := Container{
		Namespace: "default",
		Env: []string{
			"PATH=/usr/local/bin:/usr/bin",
			"LD_LIBRARY_PATH=/app/lib",
			"LD_PRELOAD=/app/libexisting.so",
			"MOCK_IB=off",
		},
	}

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin:/usr/local/bin:/usr/bin")
	require.Contains(t, adjustment.Env, "LD_LIBRARY_PATH=/opt/nvml-mock/driver/usr/lib64:/app/lib")
	require.Contains(t, adjustment.Env, "LD_PRELOAD=/app/libexisting.so:/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:/opt/nvml-mock/driver/usr/local/lib/libpcisysfs.so.1")
	require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
	require.Contains(t, adjustment.Env, "MOCK_IB_ROOT=/opt/nvml-mock/ib")
	require.Contains(t, adjustment.Env, "MOCK_IB_PING_SOCKET=/opt/nvml-mock/run/mock-ib.sock")
	require.Contains(t, adjustment.Env, "MOCK_PCI_ROOT=/opt/nvml-mock")
	require.Contains(t, adjustment.Env, "GFD_MACHINE_TYPE_FILE=/opt/nvml-mock/driver/config/machine-type")
	// MOCK_IB=off is authored by the container and left unchanged, so the
	// plugin must NOT re-emit it — emitting untouched vars would claim NRI
	// ownership and conflict with other plugins.
	requireNoEnvKey(t, adjustment.Env, "MOCK_IB")
}

// The machine type reaches GFD as a default, so a cluster that pins its own
// file keeps it: overriding would silently retarget a deliberate choice.
func TestAdjustLeavesAnAuthoredMachineTypeFile(t *testing.T) {
	t.Parallel()

	container := Container{
		Namespace: "default",
		Env:       []string{"GFD_MACHINE_TYPE_FILE=/etc/machine-type"},
	}

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)

	requireNoEnvKey(t, adjustment.Env, "GFD_MACHINE_TYPE_FILE")
}

func TestAdjustEmitsOnlyAddedOrChangedEnv(t *testing.T) {
	t.Parallel()

	container := Container{
		Namespace: "default",
		Env: []string{
			"FOO=bar",       // untouched -> must not be emitted
			"MOCK_IB=off",   // authored default -> unchanged -> not emitted
			"PATH=/usr/bin", // prepended -> changed -> emitted
		},
	}

	adjustment, ok := Adjust(DefaultConfig(), container)
	require.True(t, ok)

	requireNoEnvKey(t, adjustment.Env, "FOO")
	requireNoEnvKey(t, adjustment.Env, "MOCK_IB")
	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin:/usr/bin")
}

func TestAdjustPrependsDefaultsWhenEnvIsUnset(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), Container{Namespace: "default"})
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin")
	require.Contains(t, adjustment.Env, "LD_LIBRARY_PATH=/opt/nvml-mock/driver/usr/lib64")
	require.Contains(t, adjustment.Env, "LD_PRELOAD=/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:/opt/nvml-mock/driver/usr/local/lib/libpcisysfs.so.1")
	require.Contains(t, adjustment.Env, "MOCK_IB=full")
}

func TestAdjustInjectsTopologyEnvWhenStaged(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HostOverlayPath = stageTopology(t)
	cfg.NodeName = "kind-worker3"

	adjustment, ok := Adjust(cfg, Container{Namespace: "default"})
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "NODE_NAME=kind-worker3")
	require.Contains(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG=/opt/nvml-mock/topology/topology.yaml")
}

// Both halves are required: without a node name the engine's per-node overlay
// has no lookup key, and without a staged document the injected path would not
// resolve inside the container.
func TestAdjustSkipsTopologyEnvWhenNotConfigured(t *testing.T) {
	t.Parallel()

	tests := map[string]func(t *testing.T) Config{
		"no node name": func(t *testing.T) Config {
			cfg := DefaultConfig()
			cfg.HostOverlayPath = stageTopology(t)
			return cfg
		},
		"no staged topology document": func(t *testing.T) Config {
			cfg := DefaultConfig()
			cfg.HostOverlayPath = t.TempDir()
			cfg.NodeName = "kind-worker3"
			return cfg
		},
	}

	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			adjustment, ok := Adjust(configure(t), Container{Namespace: "default"})
			require.True(t, ok)
			requireNoEnvKey(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG")
			requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
		})
	}
}

func TestAdjustDoesNotOverrideAuthoredNodeName(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HostOverlayPath = stageTopology(t)
	cfg.NodeName = "kind-worker3"

	adjustment, ok := Adjust(cfg, Container{
		Namespace: "default",
		Env:       []string{"NODE_NAME=authored-node"},
	})
	require.True(t, ok)

	// The container authored NODE_NAME, so the plugin must neither override it
	// nor re-emit it (which would claim ownership); the authored value simply
	// stays on the container.
	require.NotContains(t, adjustment.Env, "NODE_NAME=kind-worker3")
	requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	require.Contains(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG=/opt/nvml-mock/topology/topology.yaml")
}

// An absolute shim path is passed through, a relative one resolves against the
// container's overlay mount.
func TestShimPathsResolveRelativeEntriesAgainstTheOverlay(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Shims = []string{"driver/lib/librelative.so", "/opt/absolute/libabsolute.so"}

	adjustment, ok := Adjust(cfg, Container{Namespace: "default"})
	require.True(t, ok)
	require.Contains(t, adjustment.Env,
		"LD_PRELOAD=/opt/nvml-mock/driver/lib/librelative.so:/opt/absolute/libabsolute.so")
}
