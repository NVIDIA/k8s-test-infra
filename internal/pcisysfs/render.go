// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcisysfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options controls a single rendering pass.
type Options struct {
	// Topology is the resolved layout to render. When nil or empty, Render
	// is a no-op.
	Topology *PCIeTopology

	// Identities carries the per-device PCI identity (device_id /
	// subsystem_id) keyed by lowercased BDF. A BDF present in the topology
	// but absent here still gets attribute files with the NVIDIA vendor
	// default, so lspci never fatals on a missing `vendor`.
	Identities map[string]PCI

	// Output is the fake-root directory. The renderer writes under
	// <Output>/sys/... — Output itself is created if missing. Required when
	// Topology is non-empty; otherwise Render returns an error.
	Output string
}

// Render writes the entire tree. It is idempotent: existing directories
// are reused, existing files are truncated and rewritten, and existing
// symlinks are removed and recreated so a stale relative target does not
// linger across re-renders.
func Render(o Options) error {
	if o.Topology == nil || len(o.Topology.RootComplexes) == 0 {
		return nil
	}
	if o.Output == "" {
		return errors.New("pcisysfs render: Output is required")
	}

	root := o.Output
	if err := mkdirAll(root, "sys/bus/pci/devices"); err != nil {
		return err
	}
	if err := mkdirAll(root, "sys/devices"); err != nil {
		return err
	}

	for _, rc := range o.Topology.RootComplexes {
		if err := renderRootComplex(root, rc, o.Identities); err != nil {
			return fmt.Errorf("rendering %s: %w", rc.ID, err)
		}
	}
	return nil
}

func renderRootComplex(root string, rc RootComplex, ids map[string]PCI) error {
	rcDir := filepath.Join("sys/devices", rc.ID)
	if err := mkdirAll(root, rcDir); err != nil {
		return err
	}

	for _, bdf := range rc.Devices {
		// Normalize once: sysfs paths are case-insensitive on most
		// filesystems but tooling (libpciaccess, lspci) compares
		// strings literally, so render lowercase to match the kernel.
		bdfLC := strings.ToLower(bdf)

		devDir := filepath.Join(rcDir, bdfLC)
		if err := mkdirAll(root, devDir); err != nil {
			return err
		}
		if err := writeFile(root, filepath.Join(devDir, "numa_node"),
			fmt.Sprintf("%d\n", rc.NUMANode)); err != nil {
			return err
		}

		if err := renderDeviceAttrs(root, devDir, ids[bdfLC]); err != nil {
			return fmt.Errorf("attrs for %s: %w", bdfLC, err)
		}

		// Relative target matches what the kernel emits, so readlink()
		// consumers (realpath, deviceattribute) resolve to the same
		// canonical path they would on real Linux.
		linkPath := filepath.Join(root, "sys/bus/pci/devices", bdfLC)
		linkTarget := filepath.Join("..", "..", "..", "devices", rc.ID, bdfLC)
		if err := replaceSymlink(linkPath, linkTarget); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w",
				filepath.Join("sys/bus/pci/devices", bdfLC), linkTarget, err)
		}
	}
	return nil
}

// nvidiaVendorID is the PCI vendor ID for NVIDIA Corporation (0x10de). It is
// the fallback vendor when a device carries no identity, so lspci never fatals
// on a missing `vendor` file.
const nvidiaVendorID = 0x10de

// pciClass3DController is the sysfs `class` value for NVIDIA data-center GPUs:
// base class 0x03 (display controller), subclass 0x02 (3D controller),
// prog-if 0x00. This is how real H100/A100 boards enumerate under lspci.
const pciClass3DController = 0x030200

// pciResourceBARs is the number of "start end flags" lines a Linux kernel
// emits in a device's `resource` file (6 standard BARs + expansion ROM).
const pciResourceBARs = 7

// pciResource is the all-zero `resource` file content matching the kernel's
// `0x%016x 0x%016x 0x%016x` per-BAR layout. Zero rows mean "no BAR", which is
// truthful for a mock and keeps `lspci -v` from erroring.
var pciResource = strings.Repeat(
	"0x0000000000000000 0x0000000000000000 0x0000000000000000\n",
	pciResourceBARs,
)

// renderDeviceAttrs writes the sysfs attribute files libpci reads for a
// single device. The NVML packed identity words are unpacked as the kernel
// exposes them: device_id = (device<<16)|vendor, subsystem_id =
// (subdevice<<16)|subvendor. When no identity is known the vendor defaults
// to NVIDIA so the mandatory `vendor`/`device` files still exist.
func renderDeviceAttrs(root, devDir string, pci PCI) error {
	vendor := pci.DeviceID & 0xffff
	device := (pci.DeviceID >> 16) & 0xffff
	subVendor := pci.SubsystemID & 0xffff
	subDevice := (pci.SubsystemID >> 16) & 0xffff
	if vendor == 0 {
		vendor = nvidiaVendorID
	}
	if subVendor == 0 {
		subVendor = vendor
	}

	// libpci reads these with die-on-error; they must exist.
	writes := []struct{ name, val string }{
		{"vendor", fmt.Sprintf("0x%04x\n", vendor)},
		{"device", fmt.Sprintf("0x%04x\n", device)},
		{"class", fmt.Sprintf("0x%06x\n", pciClass3DController)},
		{"revision", "0x00\n"},
		{"irq", "0\n"},
		// Optional but cheap; lets lspci print the subsystem line.
		{"subsystem_vendor", fmt.Sprintf("0x%04x\n", subVendor)},
		{"subsystem_device", fmt.Sprintf("0x%04x\n", subDevice)},
		// The kernel emits one "start end flags" line per resource;
		// all-zero means "no BAR", which is truthful for a mock.
		{"resource", pciResource},
	}
	for _, w := range writes {
		if err := writeFile(root, filepath.Join(devDir, w.name), w.val); err != nil {
			return err
		}
	}

	// Providing a synthetic config space silences the
	// "pcilib: Cannot open .../config" warning and makes `lspci -x` render
	// a coherent header.
	return writeConfigSpace(root, filepath.Join(devDir, "config"),
		uint16(vendor), uint16(device), uint16(subVendor), uint16(subDevice))
}

// writeConfigSpace emits a minimal 256-byte PCI configuration space with the
// identity, class, and header-type fields populated. All other bytes are
// zero — enough for libpci to parse a Type 0 header without erroring.
func writeConfigSpace(root, rel string, vendor, device, subVendor, subDevice uint16) error {
	cfg := make([]byte, 256)
	binary.LittleEndian.PutUint16(cfg[0x00:], vendor)
	binary.LittleEndian.PutUint16(cfg[0x02:], device)
	// Class code at 0x09-0x0b: prog-if, subclass, base class.
	cfg[0x09] = byte(pciClass3DController & 0xff)
	cfg[0x0a] = byte((pciClass3DController >> 8) & 0xff)
	cfg[0x0b] = byte((pciClass3DController >> 16) & 0xff)
	// Header type 0x00 (normal device, single function).
	cfg[0x0e] = 0x00
	binary.LittleEndian.PutUint16(cfg[0x2c:], subVendor)
	binary.LittleEndian.PutUint16(cfg[0x2e:], subDevice)

	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
	}
	if err := os.WriteFile(full, cfg, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
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
