// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

// mountOverlay binds the staged mock driver tree into the container.
//
// This is the one step with no opt-in gate: the shims, the mock NVML config and
// the IB sysfs tree all live under this path, so every later step describes
// something reachable only through it.
func mountOverlay(cfg Config, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts, Mount{
		Source:      cfg.HostOverlayPath,
		Destination: cfg.ContainerOverlayPath,
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}
