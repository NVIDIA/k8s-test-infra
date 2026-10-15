// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"path/filepath"
	"slices"
	"strings"

	"go.uber.org/zap"
)

// launcherAdjustment gives a launcher's compute container the bundle its guest
// extracts, and nothing else. libvirt and QEMU run in that container, so the
// overlay's LD_PRELOAD shims and mock library would load into the hypervisor
// rather than reach the guest. The bundle replaces the placeholder behind the
// VM's hostDisk volume, so libvirt attaches it as a disk KubeVirt defined.
func launcherAdjustment(cfg Config, container Container) (Adjustment, bool) {
	if !ValidContainerID(container.ID) {
		zap.L().Warn("KubeVirt launcher has no usable container ID; leaving it unmodified",
			zap.String("namespace", container.Namespace), zap.String("id", container.ID))

		return Adjustment{}, false
	}

	minors := allocatedMinors(container)
	if len(minors) == 0 {
		// A bundle without device entries would let the guest enumerate every
		// GPU on the node.
		zap.L().Warn("KubeVirt launcher holds a GPU allocation but no granted /dev/nvidiaN; leaving it unmodified",
			zap.String("namespace", container.Namespace), zap.String("id", container.ID))

		return Adjustment{}, false
	}

	dir := filepath.Join(cfg.GuestHostPath, container.ID)
	var mounts []Mount
	for _, placeholder := range placeholderMounts(cfg, container) {
		mounts = append(mounts, Mount{
			Source:      dir,
			Destination: placeholder.Destination,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
		})
	}
	zap.L().Debug("KubeVirt launcher gets a guest bundle only",
		zap.String("namespace", container.Namespace), zap.String("id", container.ID),
		zap.Ints("minors", minors), zap.Int("volumes", len(mounts)))

	return Adjustment{Guest: &GuestBundle{Dir: dir, Minors: minors, Mounts: mounts}}, true
}

// placeholderMounts are the container's mounts of the guest placeholder
// directory, one per bundle volume the VM declares.
func placeholderMounts(cfg Config, container Container) []Mount {
	var mounts []Mount
	for _, mount := range container.Mounts {
		if filepath.Clean(mount.Source) == filepath.Clean(cfg.GuestPlaceholderPath) {
			mounts = append(mounts, mount)
		}
	}

	return mounts
}

// allocatedMinors lists the GPUs the scheduler gave the container, by the
// evidence hasRawGPUAllocation accepts: a numbered node with its own exact
// cgroup grant. containerd resolves CDI devices into the spec before NRI sees
// it, so this covers the device plugin's CDI strategies too.
func allocatedMinors(container Container) []int {
	var minors []int
	for _, device := range container.IncomingDevices {
		minor, ok := gpuMinor(device.Path)
		if !ok || device.Type != "c" || !hasExactDeviceGrant(device, container.DeviceRules) {
			continue
		}
		if !slices.Contains(minors, minor) {
			minors = append(minors, minor)
		}
	}
	slices.Sort(minors)

	return minors
}

// ValidContainerID reports whether id can name a directory under the guest
// bundle root. Bundles are removed recursively by that name, so an ID carrying
// a separator or a parent reference must never reach the filesystem.
func ValidContainerID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}
