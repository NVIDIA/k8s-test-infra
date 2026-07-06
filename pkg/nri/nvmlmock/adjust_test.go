// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvmlmock

import (
	"os"
	"path/filepath"
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
	require.Contains(t, adjustment.Env, "MOCK_IB=off")
	require.Contains(t, adjustment.Env, "MOCK_IB_ROOT=/opt/nvml-mock/ib")
	require.Contains(t, adjustment.Env, "MOCK_IB_PING_SOCKET=/opt/nvml-mock/run/mock-ib.sock")
	require.Contains(t, adjustment.Env, "MOCK_PCI_ROOT=/opt/nvml-mock")
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
