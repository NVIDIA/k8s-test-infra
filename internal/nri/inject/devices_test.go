// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDiscoverDevicesSkipsChannelDirectory pins a defect that predates IMEX
// channel injection. imex.mockChannels (#541) mknods the channel nodes into a
// nvidia-caps-imex-channels DIRECTORY inside the same device root the plugin
// scans, and that directory name matches the "nvidia" prefix filter. Injecting
// a directory as a LinuxDevice is not a device node, so the runtime cannot
// create it in the container.
func TestDiscoverDevicesSkipsChannelDirectory(t *testing.T) {
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia0")
	require.NoError(t, os.Mkdir(filepath.Join(deviceRoot, "nvidia-caps-imex-channels"), 0o755))

	devices, err := discoverDevices(deviceRoot)
	require.NoError(t, err)
	require.Equal(t, []Device{
		{HostPath: filepath.Join(deviceRoot, "nvidia0"), Path: "/dev/nvidia0"},
	}, devices)
}

// Ordering is by container path so an adjustment is reproducible; os.ReadDir's
// own order is filesystem-dependent.
func TestDiscoverDevicesSortsByContainerPath(t *testing.T) {
	t.Parallel()

	deviceRoot := stageDeviceNodes(t, "nvidia2", "nvidia0", "nvidiactl", "nvidia1")

	devices, err := discoverDevices(deviceRoot)
	require.NoError(t, err)

	paths := make([]string, 0, len(devices))
	for _, device := range devices {
		paths = append(paths, device.Path)
	}
	require.Equal(t, []string{"/dev/nvidia0", "/dev/nvidia1", "/dev/nvidia2", "/dev/nvidiactl"}, paths)
}

func TestDiscoverImexChannelsMapsOntoTheKernelPath(t *testing.T) {
	t.Parallel()

	channelRoot := stageDeviceNodes(t, "channel0", "not-a-channel")

	channels, err := discoverImexChannels(channelRoot)
	require.NoError(t, err)
	require.Equal(t, []Device{
		{HostPath: filepath.Join(channelRoot, "channel0"), Path: "/dev/nvidia-caps-imex-channels/channel0"},
	}, channels)
}
