// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package render writes a fake PCI sysfs tree from a
// [config.PCIeTopology] specification.
//
// The layout mimics what real Linux kernels expose to userspace, so any
// consumer that resolves "PCIe root complex" via a single readlink + path
// parse (e.g. the k8s deviceattribute library used by the NVIDIA DRA
// driver) gets the right answer when pointed at the rendered tree:
//
//	<output>/sys/bus/pci/devices/0000:07:00.0 ->
//	    ../../../devices/pci0000:00/0000:07:00.0
//	<output>/sys/devices/pci0000:00/0000:07:00.0/numa_node    # "0"
//
// The tree only carries what topology resolution needs (symlinks +
// numa_node). It is *not* a full sysfs simulation — device class files
// (vendor, device, config, resource, ...) are out of scope; callers that
// need richer attributes can stack additional generators on top of this
// tree without conflict.
package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
)

// Options controls a single rendering pass.
type Options struct {
	// Topology is the resolved layout to render. Callers typically pass
	// `profile.EffectiveTopology()` so empty `pcie_topology:` blocks
	// still produce a flat default tree.
	Topology *config.PCIeTopology

	// Output is the fake-root directory. The renderer writes under
	// <Output>/sys/... — Output itself is created if missing.
	Output string
}

// Render writes the entire tree. It is idempotent: existing directories
// are reused, existing files are truncated and rewritten, and existing
// symlinks are removed and recreated so a stale relative target does not
// linger across re-renders.
func Render(o Options) error {
	if o.Topology == nil || len(o.Topology.RootComplexes) == 0 {
		// Nothing to do — caller decided to render a profile with no
		// declared topology and no devices. Treat as a no-op so the
		// renderer can be invoked unconditionally from setup.sh.
		return nil
	}
	if o.Output == "" {
		return fmt.Errorf("pcisysfs render: Output is required")
	}

	root := o.Output
	if err := mkdirAll(root, "sys/bus/pci/devices"); err != nil {
		return err
	}
	if err := mkdirAll(root, "sys/devices"); err != nil {
		return err
	}

	for _, rc := range o.Topology.RootComplexes {
		if err := renderRootComplex(root, rc); err != nil {
			return fmt.Errorf("rendering %s: %w", rc.ID, err)
		}
	}
	return nil
}

func renderRootComplex(root string, rc config.RootComplex) error {
	rcDir := filepath.Join("sys/devices", rc.ID)
	if err := mkdirAll(root, rcDir); err != nil {
		return err
	}

	for _, bdf := range rc.Devices {
		// Normalize once: sysfs paths are case-insensitive on most
		// filesystems but tooling (libpciaccess, lspci) compares
		// strings literally, so render lowercase to match the kernel.
		bdfLC := strings.ToLower(bdf)

		// 1. /sys/devices/<root>/<bdf>/numa_node
		devDir := filepath.Join(rcDir, bdfLC)
		if err := mkdirAll(root, devDir); err != nil {
			return err
		}
		if err := writeFile(root, filepath.Join(devDir, "numa_node"),
			fmt.Sprintf("%d\n", rc.NUMANode)); err != nil {
			return err
		}

		// 2. /sys/bus/pci/devices/<bdf> -> ../../../devices/<root>/<bdf>
		// Relative target matches what the kernel emits, so any
		// readlink() consumer (`realpath`, deviceattribute, etc.)
		// resolves to the same canonical path it would on real Linux.
		linkPath := filepath.Join(root, "sys/bus/pci/devices", bdfLC)
		linkTarget := filepath.Join("..", "..", "..", "devices", rc.ID, bdfLC)
		if err := replaceSymlink(linkPath, linkTarget); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w",
				filepath.Join("sys/bus/pci/devices", bdfLC), linkTarget, err)
		}
	}
	return nil
}

// replaceSymlink ensures `path` is a symlink pointing at `target`,
// removing any stale file/symlink in the way first. Symlink atomicity
// matters less here than predictable re-render behavior: a previous run
// may have left a symlink to a different root complex if the topology
// was edited between runs.
func replaceSymlink(path, target string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	// `os.Remove` returns nil if the path doesn't exist on some
	// platforms but errors on others; tolerate both shapes.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear %s: %w", path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("create symlink %s: %w", path, err)
	}
	return nil
}

func mkdirAll(root, rel string) error {
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", rel, err)
	}
	return nil
}

func writeFile(root, rel, contents string) error {
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}
