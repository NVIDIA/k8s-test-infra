// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
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
	}
}
