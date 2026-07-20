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
// Beyond topology resolution (symlinks + numa_node), the tree also carries
// the PCI identity attribute files that userspace PCI tooling reads:
// `vendor`, `device`, `subsystem_vendor`, `subsystem_device`, `class`,
// `revision`, `irq`, and a synthetic binary `config` space. This is what
// lets `lspci` enumerate the mock GPUs inside the pod (via the
// libpcimocksys.so redirector) instead of failing with "Cannot open
// .../vendor". Attribute-bearing [Device] entries (e.g. synthesized Mellanox
// NICs) render the same device-class files so a NIC also looks real to PCI
// scanners like NFD. It is still *not* a full sysfs simulation — resource
// ranges, capabilities, and driver bindings are out of scope.
package render

import (
	"encoding/binary"
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

	// Identities carries the per-device PCI identity (device_id /
	// subsystem_id) keyed by lowercased BDF, as returned by
	// config.Profile.DeviceIdentities(). It is the source of the
	// lspci-visible attribute files. A BDF present in the topology but
	// absent here still gets attribute files rendered with the NVIDIA
	// vendor default, so lspci never fatals on a missing `vendor`.
	Identities map[string]config.PCI

	// Output is the fake-root directory. The renderer writes under
	// <Output>/sys/... — Output itself is created if missing.
	//
	// When Topology is nil or has no root complexes, Render is a no-op
	// even if Output is empty (so setup.sh can invoke the renderer
	// unconditionally). A non-nil Topology with a non-empty Output is
	// required; otherwise Render returns an error.
	Output string

	// Devices are self-contained PCI devices rendered in addition to
	// Topology. Unlike topology entries (symlink + numa_node only),
	// each Device can carry attribute files. Used for synthesized NIC
	// entries that consumers match on vendor/class.
	Devices []Device
}

// Attrs holds the PCI attribute files a device exposes. Empty fields are
// skipped so callers only materialize what they set. Values use the kernel
// sysfs format (lowercase "0x" + hex), e.g. "0x15b3".
type Attrs struct {
	Vendor          string
	Device          string
	Class           string
	SubsystemVendor string
	SubsystemDevice string
}

// Device is a self-contained PCI device: symlink + numa_node (like the
// topology path) plus attribute files. Used for synthesized NIC entries that
// consumers (NFD's pci.device source) match on vendor/class.
type Device struct {
	BDF         string
	RootComplex string
	NUMANode    int
	Attrs       Attrs
}

// Render writes the entire tree. It is idempotent: existing directories
// are reused, existing files are truncated and rewritten, and existing
// symlinks are removed and recreated so a stale relative target does not
// linger across re-renders.
func Render(o Options) error {
	if (o.Topology == nil || len(o.Topology.RootComplexes) == 0) && len(o.Devices) == 0 {
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

	if o.Topology != nil {
		for _, rc := range o.Topology.RootComplexes {
			if err := renderRootComplex(root, rc, o.Identities); err != nil {
				return fmt.Errorf("rendering %s: %w", rc.ID, err)
			}
		}
	}

	for _, d := range o.Devices {
		if err := renderDevice(root, d); err != nil {
			return fmt.Errorf("rendering device %s: %w", d.BDF, err)
		}
	}
	return nil
}

// renderDevice writes a self-contained PCI device: its root-complex dir,
// numa_node, attribute files, and the /sys/bus/pci/devices relative symlink.
func renderDevice(root string, d Device) error {
	bdfLC := strings.ToLower(d.BDF)
	devDir := filepath.Join("sys/devices", d.RootComplex, bdfLC)
	if err := mkdirAll(root, devDir); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(devDir, "numa_node"),
		fmt.Sprintf("%d\n", d.NUMANode)); err != nil {
		return err
	}
	for _, kv := range []struct{ name, val string }{
		{"vendor", d.Attrs.Vendor},
		{"device", d.Attrs.Device},
		{"class", d.Attrs.Class},
		{"subsystem_vendor", d.Attrs.SubsystemVendor},
		{"subsystem_device", d.Attrs.SubsystemDevice},
	} {
		if kv.val == "" {
			continue
		}
		if err := writeFile(root, filepath.Join(devDir, kv.name), kv.val+"\n"); err != nil {
			return err
		}
	}
	linkPath := filepath.Join(root, "sys/bus/pci/devices", bdfLC)
	linkTarget := filepath.Join("..", "..", "..", "devices", d.RootComplex, bdfLC)
	return replaceSymlink(linkPath, linkTarget)
}

func renderRootComplex(root string, rc config.RootComplex, ids map[string]config.PCI) error {
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

		// 1b. PCI identity attribute files (vendor, device, class, ...)
		// so lspci and other libpci consumers can enumerate the device.
		if err := renderDeviceAttrs(root, devDir, ids[bdfLC]); err != nil {
			return fmt.Errorf("attrs for %s: %w", bdfLC, err)
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

// nvidiaVendorID is the PCI vendor ID for NVIDIA Corporation (0x10de). It is
// the fallback vendor when a profile omits device_id, so a rendered device
// always presents a well-formed `vendor` file and lspci never fatals.
const nvidiaVendorID = 0x10de

// pciClass3DController is the sysfs `class` value for NVIDIA data-center GPUs:
// base class 0x03 (display controller), subclass 0x02 (3D controller),
// prog-if 0x00. This is how real H100/A100 boards enumerate under lspci
// ("3D controller: NVIDIA Corporation ...").
const pciClass3DController = 0x030200

// pciResourceBARs is the number of "start end flags" lines a Linux kernel
// emits in a device's `resource` file (6 standard BARs + expansion ROM).
const pciResourceBARs = 7

// pciResourceFile builds an all-zero `resource` table matching the kernel's
// `0x%016x 0x%016x 0x%016x` per-BAR layout. Zero rows mean "no BAR", so
// `lspci -v` prints the device without inventing memory ranges.
func pciResourceFile() string {
	const zeroRow = "0x0000000000000000 0x0000000000000000 0x0000000000000000\n"
	return strings.Repeat(zeroRow, pciResourceBARs)
}

// renderDeviceAttrs writes the sysfs attribute files libpci reads for a
// single device. The NVML packed identity words are unpacked as the kernel
// exposes them: device_id = (device<<16)|vendor, subsystem_id =
// (subdevice<<16)|subvendor. When no identity is known the vendor defaults
// to NVIDIA so the mandatory `vendor`/`device` files still exist.
func renderDeviceAttrs(root, devDir string, pci config.PCI) error {
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

	attrs := map[string]string{
		// libpci reads these with die-on-error; they must exist.
		"vendor":   fmt.Sprintf("0x%04x\n", vendor),
		"device":   fmt.Sprintf("0x%04x\n", device),
		"class":    fmt.Sprintf("0x%06x\n", pciClass3DController),
		"revision": "0x00\n",
		"irq":      "0\n",
		// Optional but cheap; lets lspci print the subsystem line.
		"subsystem_vendor": fmt.Sprintf("0x%04x\n", subVendor),
		"subsystem_device": fmt.Sprintf("0x%04x\n", subDevice),
		// BAR table read by `lspci -v` (via fopen). The kernel emits one
		// "start end flags" line per resource; all-zero means "no BAR",
		// which is truthful for a mock and keeps lspci from erroring.
		"resource": pciResourceFile(),
	}
	for name, contents := range attrs {
		if err := writeFile(root, filepath.Join(devDir, name), contents); err != nil {
			return err
		}
	}

	// Synthetic binary config space. lspci reads the 64-byte header first;
	// providing it silences the "pcilib: Cannot open .../config" warning and
	// makes `lspci -x` render a coherent header.
	if err := writeConfigSpace(root, filepath.Join(devDir, "config"),
		uint16(vendor), uint16(device), uint16(subVendor), uint16(subDevice)); err != nil {
		return err
	}
	return nil
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
