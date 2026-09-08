// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
)

// kernelPath pairs a directory the node agent stages with the kernel path a
// consumer reads it at. The destinations are fixed: they are what the consumers
// compile in, which is the whole reason for serving them here.
type kernelPath struct {
	relPath     string
	destination string
	// options differ in one respect only, and it matters: nodev makes the
	// kernel refuse to open device nodes on the mount, so a directory whose
	// entire content is device nodes cannot carry it.
	options []string
}

// staged reports whether the agent has rendered the path yet.
//
// Nothing orders this plugin's DaemonSet after the agent's, and a mount with a
// missing source fails creation for the whole pod rather than for the surface
// it belongs to — so an unrendered path degrades the injection instead.
func staged(cfg Config, p kernelPath) bool {
	_, err := os.Stat(p.source(cfg))

	return err == nil
}

func (p kernelPath) source(cfg Config) string {
	return filepath.Join(cfg.HostOverlayPath, p.relPath)
}

// mountKernelPath emits the mount for one staged path, reporting the source it
// used and whether anything was emitted at all.
func mountKernelPath(cfg Config, p kernelPath, adjustment *Adjustment) (string, bool) {
	if !staged(cfg, p) {
		return "", false
	}

	source := p.source(cfg)
	adjustment.Mounts = append(adjustment.Mounts, Mount{
		Source:      source,
		Destination: p.destination,
		Type:        "bind",
		Options:     p.options,
	})

	return source, true
}
