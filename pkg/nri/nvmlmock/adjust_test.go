// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvmlmock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdjustPlainContainerAddsOverlayAndEnvironment(t *testing.T) {
	cfg := DefaultConfig()
	container := Container{
		Namespace: "default",
		Env: []string{
			"PATH=/usr/local/bin:/usr/bin",
			"LD_LIBRARY_PATH=/app/lib",
			"LD_PRELOAD=/app/libexisting.so",
			"MOCK_IB=off",
		},
	}

	adjustment, ok, err := Adjust(cfg, container)
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin:/usr/local/bin:/usr/bin")
	require.Contains(t, adjustment.Env, "LD_LIBRARY_PATH=/opt/nvml-mock/driver/usr/lib64:/app/lib")
	require.Contains(t, adjustment.Env, "LD_PRELOAD=/app/libexisting.so:/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:/opt/nvml-mock/driver/usr/local/lib/libpcimocksys.so.1")
	require.Contains(t, adjustment.Env, "MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml")
	require.Contains(t, adjustment.Env, "MOCK_IB_ROOT=/opt/nvml-mock/ib")
	require.Contains(t, adjustment.Env, "MOCK_IB_PING_SOCKET=/opt/nvml-mock/run/mock-ib.sock")
	require.Contains(t, adjustment.Env, "MOCK_PCI_ROOT=/opt/nvml-mock")
	// MOCK_IB=off is authored by the container and left unchanged (setDefaultEnv
	// is a no-op), so the plugin must NOT re-emit it — emitting untouched vars
	// would claim NRI ownership and conflict with other plugins.
	requireNoEnvKey(t, adjustment.Env, "MOCK_IB")
}

func TestAdjustEmitsOnlyAddedOrChangedEnv(t *testing.T) {
	container := Container{
		Namespace: "default",
		Env: []string{
			"FOO=bar",       // untouched -> must not be emitted
			"MOCK_IB=off",   // authored default -> unchanged -> not emitted
			"PATH=/usr/bin", // prepended -> changed -> emitted
		},
	}

	adjustment, ok, err := Adjust(DefaultConfig(), container)
	require.NoError(t, err)
	require.True(t, ok)

	requireNoEnvKey(t, adjustment.Env, "FOO")
	requireNoEnvKey(t, adjustment.Env, "MOCK_IB")
	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin:/usr/bin")
}

func TestAdjustInjectsTopologyEnvWhenStaged(t *testing.T) {
	overlayHost := t.TempDir()
	topoDir := filepath.Join(overlayHost, "topology")
	require.NoError(t, os.MkdirAll(topoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))

	cfg := DefaultConfig()
	cfg.HostOverlayPath = overlayHost
	cfg.NodeName = "kind-worker3"

	adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "NODE_NAME=kind-worker3")
	require.Contains(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG=/opt/nvml-mock/topology/topology.yaml")
}

func TestAdjustSkipsTopologyEnvWhenNotConfigured(t *testing.T) {
	t.Run("no node name", func(t *testing.T) {
		overlayHost := t.TempDir()
		topoDir := filepath.Join(overlayHost, "topology")
		require.NoError(t, os.MkdirAll(topoDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))

		cfg := DefaultConfig()
		cfg.HostOverlayPath = overlayHost

		adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
		require.NoError(t, err)
		require.True(t, ok)
		requireNoEnvKey(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG")
		requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	})

	t.Run("no staged topology document", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.HostOverlayPath = t.TempDir()
		cfg.NodeName = "kind-worker3"

		adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
		require.NoError(t, err)
		require.True(t, ok)
		requireNoEnvKey(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG")
		requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	})
}

func TestAdjustDoesNotOverrideAuthoredNodeName(t *testing.T) {
	overlayHost := t.TempDir()
	topoDir := filepath.Join(overlayHost, "topology")
	require.NoError(t, os.MkdirAll(topoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topoDir, "topology.yaml"), []byte("version: 1\n"), 0o644))

	cfg := DefaultConfig()
	cfg.HostOverlayPath = overlayHost
	cfg.NodeName = "kind-worker3"

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		Env:       []string{"NODE_NAME=authored-node"},
	})
	require.NoError(t, err)
	require.True(t, ok)

	// The container authored NODE_NAME, so the plugin must neither override it
	// nor re-emit it (which would claim ownership); the authored value simply
	// stays on the container. It must NOT be replaced with the plugin's node.
	require.NotContains(t, adjustment.Env, "NODE_NAME=kind-worker3")
	requireNoEnvKey(t, adjustment.Env, "NODE_NAME")
	require.Contains(t, adjustment.Env, "MOCK_TOPOLOGY_CONFIG=/opt/nvml-mock/topology/topology.yaml")
}

func requireNoEnvKey(t *testing.T, env []string, key string) {
	t.Helper()
	for _, item := range env {
		if name, _, ok := strings.Cut(item, "="); ok && name == key {
			t.Fatalf("expected env not to contain key %q, got %q", key, item)
		}
	}
}

func TestAdjustDeviceOptInFailsOpenWhenTreeMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DeviceHostPath = filepath.Join(t.TempDir(), "does-not-exist")

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices": "true",
		},
	})
	// Degrade to overlay-only injection: no error, no devices, but the overlay
	// mount is still applied so the pod isn't blocked from starting.
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}

func TestAdjustSkipsOptOutExcludedNamespaceAndExistingMount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExcludedNamespaces = []string{"kube-system", "nvml-mock"}

	tests := map[string]Container{
		"opt out annotation": {
			Namespace: "default",
			PodAnnotations: map[string]string{
				"nvml-mock.nvidia.com/inject": "false",
			},
		},
		"excluded namespace": {
			Namespace: "kube-system",
		},
		"existing overlay mount": {
			Namespace: "default",
			Mounts: []Mount{
				{Destination: "/opt/nvml-mock"},
			},
		},
	}

	for name, container := range tests {
		t.Run(name, func(t *testing.T) {
			adjustment, ok, err := Adjust(cfg, container)
			require.NoError(t, err)
			require.False(t, ok)
			require.Empty(t, adjustment)
		})
	}
}

func TestAdjustPrependsDefaultsWhenEnvIsUnset(t *testing.T) {
	adjustment, ok, err := Adjust(DefaultConfig(), Container{Namespace: "default"})
	require.NoError(t, err)
	require.True(t, ok)

	require.Contains(t, adjustment.Env, "PATH=/opt/nvml-mock/driver/usr/bin")
	require.Contains(t, adjustment.Env, "LD_LIBRARY_PATH=/opt/nvml-mock/driver/usr/lib64")
	require.Contains(t, adjustment.Env, "LD_PRELOAD=/opt/nvml-mock/driver/usr/local/lib/libibmockumad.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1:/opt/nvml-mock/driver/usr/local/lib/libibmocksys.so.1:/opt/nvml-mock/driver/usr/local/lib/libpcimocksys.so.1")
	require.Contains(t, adjustment.Env, "MOCK_IB=full")
}

func TestAdjustDeviceOptInAddsNvidiaDeviceEntries(t *testing.T) {
	deviceRoot := t.TempDir()
	for _, name := range []string{"nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm", "not-nvidia"} {
		require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, name), []byte{}, 0o644))
	}

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
		{HostPath: filepath.Join(deviceRoot, "nvidia1"), Path: "/dev/nvidia1"},
		{HostPath: filepath.Join(deviceRoot, "nvidiactl"), Path: "/dev/nvidiactl"},
		{HostPath: filepath.Join(deviceRoot, "nvidia-uvm"), Path: "/dev/nvidia-uvm"},
	}, adjustment.Devices)
}

func TestDiscoverDevicesSkipsDirectories(t *testing.T) {
	// setup.sh stages the IMEX channel nodes in a subdirectory
	// (nvidia-caps-imex-channels) inside the device host path. Its name starts
	// with "nvidia", so without an IsDir guard discoverDevices would emit a
	// bogus device for the directory, which nriDevice later rejects as "not a
	// character device". Ensure the directory is skipped.
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(deviceRoot, "nvidia-caps-imex-channels"), 0o755))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
	}, adjustment.Devices)
	require.NotContains(t, adjustment.Devices, Device{
		HostPath: filepath.Join(deviceRoot, "nvidia-caps-imex-channels"),
		Path:     "/dev/nvidia-caps-imex-channels",
	})
}

func TestAdjustImexChannelsOptInAddsChannelDevices(t *testing.T) {
	channelRoot := t.TempDir()
	for _, name := range []string{"channel0", "channel1", "channel2", "not-a-channel"} {
		require.NoError(t, os.WriteFile(filepath.Join(channelRoot, name), []byte{}, 0o644))
	}

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
		{HostPath: filepath.Join(channelRoot, "channel1"), Path: "/dev/nvidia-caps-imex-channels/channel1"},
		{HostPath: filepath.Join(channelRoot, "channel2"), Path: "/dev/nvidia-caps-imex-channels/channel2"},
	}, adjustment.Devices)
}

func TestAdjustImexChannelsNotRequestedInjectsNoChannels(t *testing.T) {
	channelRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(channelRoot, "channel0"), []byte{}, 0o644))

	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok, err := Adjust(cfg, Container{Namespace: "default"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
}

func TestAdjustImexChannelsFailsOpenWhenTreeMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ImexChannelHostPath = filepath.Join(t.TempDir(), "does-not-exist")

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, adjustment.Devices)
	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock",
		Destination: "/opt/nvml-mock",
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}

func TestAdjustDevicesAndImexChannelsAreAdditive(t *testing.T) {
	deviceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(deviceRoot, "nvidia0"), []byte{}, 0o644))
	channelRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(channelRoot, "channel0"), []byte{}, 0o644))

	cfg := DefaultConfig()
	cfg.DeviceHostPath = deviceRoot
	cfg.ImexChannelHostPath = channelRoot

	adjustment, ok, err := Adjust(cfg, Container{
		Namespace: "default",
		PodAnnotations: map[string]string{
			"nvml-mock.nvidia.com/devices":       "true",
			"nvml-mock.nvidia.com/imex-channels": "true",
		},
	})
	require.NoError(t, err)
	require.True(t, ok)

	require.ElementsMatch(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
	}, adjustment.Devices)
}

func TestDefaultConfigSetsImexChannelDefaults(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, "nvml-mock.nvidia.com/imex-channels", cfg.ImexChannelAnnotation)
	require.Equal(t, "/var/lib/nvml-mock/driver/dev/nvidia-caps-imex-channels", cfg.ImexChannelHostPath)
}
