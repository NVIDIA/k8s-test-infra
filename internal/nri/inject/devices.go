// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// discoverDevices lists the mock /dev/nvidia* nodes staged in the overlay.
func discoverDevices(deviceHostPath string) ([]Device, error) {
	return scanDeviceDir(deviceHostPath, "nvidia", "/dev")
}

// discoverImexChannels lists the mock IMEX channel nodes staged by
// imex.mockChannels, mapping them onto the fixed kernel path consumers expect.
func discoverImexChannels(channelHostPath string) ([]Device, error) {
	return scanDeviceDir(channelHostPath, imexChannelPrefix, imexChannelContainerDir)
}

const imexChannelPrefix = "channel"

// scanDeviceDir collects the device nodes directly under hostDir whose names
// carry prefix, mapping each onto containerDir inside the container.
//
// Directories are skipped: hostDir is a device root, not a tree to recurse, and
// the mock device root holds a nvidia-caps-imex-channels DIRECTORY that matches
// the "nvidia" prefix. Handing a directory to the runtime as a device node
// cannot work, so it must never enter the adjustment.
func scanDeviceDir(hostDir, prefix, containerDir string) ([]Device, error) {
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		return nil, err
	}

	devices := make([]Device, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		devices = append(devices, Device{
			HostPath: filepath.Join(hostDir, name),
			Path:     filepath.Join(containerDir, name),
		})
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Path < devices[j].Path
	})
	return devices, nil
}
