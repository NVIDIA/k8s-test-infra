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

// ibKernelPaths pairs each staged directory with the kernel path a consumer
// reads it at. The destinations are fixed: they are what the consumers compile
// in, which is the whole reason for serving them here.
var ibKernelPaths = []struct {
	relPath     string
	destination string
	// options differ in one respect only, and it matters: nodev makes the
	// kernel refuse to open device nodes on the mount, so the directory whose
	// entire content is device nodes cannot carry it.
	options []string
}{
	{
		relPath:     ibSysClassRelPath,
		destination: "/sys/class",
		options:     []string{"rbind", "ro", "nosuid", "nodev"},
	},
	{
		relPath:     ibDevicesRelPath,
		destination: "/dev/infiniband",
		options:     []string{"rbind", "ro", "nosuid"},
	},
}

// mountIBKernelPaths serves the rendered InfiniBand tree where consumers
// actually read it. The shims redirect these paths for libc callers, but Go's
// os package issues openat directly, so a Go consumer reads the node's real
// /sys and finds no InfiniBand at all — the same gap #673 closed for the PCI
// tree. It cannot reuse that channel: the RDMA device plugin requests no GPU,
// so nothing ever resolves a CDI spec for its pod.
//
// Recursive by necessity. The agent bind-mounts the node's own sysfs classes
// inside the class directory, and a plain bind would present every one of them
// empty — including sys/class/net, which is what an RDMA consumer resolves each
// HCA through.
//
// Each is emitted only once staged: nothing orders this plugin's DaemonSet
// after the agent's, and a mount with a missing source fails creation for the
// whole pod rather than for the surface it belongs to.
func mountIBKernelPaths(cfg Config, adjustment *Adjustment) {
	for _, p := range ibKernelPaths {
		source := filepath.Join(cfg.HostOverlayPath, p.relPath)
		if _, err := os.Stat(source); err != nil {
			continue
		}

		adjustment.Mounts = append(adjustment.Mounts, Mount{
			Source:      source,
			Destination: p.destination,
			Type:        "bind",
			Options:     p.options,
		})

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
			Options:     []string{"rbind", "ro", "nosuid", "nodev"},
		})
	}
}
