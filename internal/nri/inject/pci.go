// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import "github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"

// pciKernelPaths is the pcibus renderer's tree at the paths a consumer reads it
// at. The bus directory is flat symlinks into the hierarchy, so the two are
// served together or not at all.
var pciKernelPaths = []kernelPath{
	{
		relPath:     pcisysfs.SysDevicesRelPath,
		destination: "/" + pcisysfs.SysDevicesRelPath,
		options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
	},
	{
		relPath:     pcisysfs.PCIDevicesRelPath,
		destination: "/" + pcisysfs.PCIDevicesRelPath,
		options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
	},
}

// mountPCIKernelPaths serves the mock PCI tree to consumers CDI cannot reach.
//
// #673 serves this tree through CDI, which covers anything requesting a GPU.
// The RDMA device plugin requests none: it finds its HCAs by enumerating the
// bus, matching the Mellanox vendor and reading the infiniband directory of
// each device it matched — all of it from a pod for which no CDI spec is ever
// resolved. Left to the node's real bus it finds no Mellanox device at all and
// advertises nothing, which is indistinguishable from the simulation being
// absent.
//
// Whatever also arrives through CDI is bound twice at the same destination.
// That is deliberate: the mount is identical either way, and the alternative is
// this plugin reasoning about which specs the runtime resolved for a container
// it is being asked to adjust.
func mountPCIKernelPaths(cfg Config, adjustment *Adjustment) {
	for _, p := range pciKernelPaths {
		if !staged(cfg, p) {
			return
		}
	}

	for _, p := range pciKernelPaths {
		_, _ = mountKernelPath(cfg, p, adjustment)
	}

	mountReproducedNetdevs(cfg, adjustment)
}

// mountReproducedNetdevs serves the node's own netdev hierarchy back over the
// mountpoint the renderer left inside the mock one.
//
// The hierarchy mount hides the node's, and every /sys/class/net entry is a
// relative symlink into it — so without this, reading an interface attribute
// through the class directory fails for the node's interfaces and for the
// simulated HCAs' links alike, which is how a consumer decides an HCA is
// usable. The node's hierarchy is the right source for both: the HCAs' links
// are real dummy netdevs the agent created in the node's namespace.
//
// Appended last, since it lands inside the hierarchy mount it must not precede.
func mountReproducedNetdevs(cfg Config, adjustment *Adjustment) {
	mountpoint := kernelPath{
		relPath:     pcisysfs.VirtualNetRelPath,
		destination: "/" + pcisysfs.VirtualNetRelPath,
	}
	if !staged(cfg, mountpoint) {
		return
	}

	adjustment.Mounts = append(adjustment.Mounts, Mount{
		Source:      mountpoint.destination,
		Destination: mountpoint.destination,
		Type:        "bind",
		Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
	})
}
