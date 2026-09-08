// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
)

const (
	// ibSysClassRelPath is the class directory the node agent's ib simulator
	// renders, resolved against the host overlay path. It carries the mock
	// InfiniBand classes and, bind-mounted beneath them, the node's own.
	ibSysClassRelPath = "ib/sys/class"
	// ibDevicesRelPath holds the InfiniBand char devices the same simulator
	// mknods.
	ibDevicesRelPath = "ib/dev/infiniband"
)

var ibKernelPaths = []kernelPath{
	{
		relPath:     ibSysClassRelPath,
		destination: "/sys/class",
		options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
	},
	{
		relPath:     ibDevicesRelPath,
		destination: "/dev/infiniband",
		options:     []string{"bind", "rprivate", "ro", "nosuid"},
	},
}

// mountIBKernelPaths serves the rendered InfiniBand tree where consumers
// actually read it. The shims redirect these paths for libc callers, but Go's
// os package issues openat directly, so a Go consumer reads the node's real
// /sys and finds no InfiniBand at all — the same gap #673 closed for the PCI
// tree. It cannot reuse that channel: the RDMA device plugin requests no GPU,
// so nothing ever resolves a CDI spec for its pod.
//
// Private and non-recursive, which is not incidental. Binding a shared mount
// makes the copy a peer of it, so mounts the runtime makes underneath propagate
// back onto the node's own path; the node's /sys is shared, and a pod using
// bidirectional propagation carries the escape out of the container entirely.
// Every class served below then reappeared on the node, and the next container
// to bind that path copied the accumulated stack, doubling it per container
// generation until the node's mount table was large enough that containerd
// could no longer tear pods down. Private keeps the copy out of that group, and
// nothing here needs recursion: the node's classes are served back as mounts of
// their own rather than as submounts of this tree.
//
func mountIBKernelPaths(cfg Config, adjustment *Adjustment) {
	for _, p := range ibKernelPaths {
		source, ok := mountKernelPath(cfg, p, adjustment)
		if !ok {
			continue
		}

		if p.relPath == ibSysClassRelPath {
			mountReproducedClasses(source, adjustment)
		}
	}
}

// mountReproducedClasses attaches the node's own classes over the empty
// mountpoints the renderer left for them, restoring what the class mount above
// would otherwise hide — the node's sys/class/net most of all, since an RDMA
// consumer resolves every HCA through an interface listed there.
//
// The runtime performs these, which is the point: a mount the agent made in its
// own namespace would need CAP_SYS_ADMIN and bidirectional propagation before
// anything here could see it, whereas assembling the container's filesystem is
// already this mount's job.
//
// Ordering matters. These come after the class mount they land inside, so they
// are appended while it is being emitted rather than in a separate pass.
func mountReproducedClasses(treeClass string, adjustment *Adjustment) {
	entries, err := os.ReadDir(treeClass)
	if err != nil {
		// The class mount is already emitted and stands on its own; the node's
		// other classes are what a container loses, not the simulated HCAs.
		return
	}

	owned := make(map[string]struct{}, len(sysfs.MockOwnedClasses))
	for _, c := range sysfs.MockOwnedClasses {
		owned[c] = struct{}{}
	}

	for _, e := range entries {
		if _, isOwned := owned[e.Name()]; isOwned {
			continue
		}

		// Source and destination are the same path: the node's class, served
		// back at the location the class mount just covered.
		class := filepath.Join("/sys/class", e.Name())
		adjustment.Mounts = append(adjustment.Mounts, Mount{
			Source:      class,
			Destination: class,
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		})
	}
}
