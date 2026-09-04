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

// The two directories that together make the tree readable. Exported because a
// server naming different paths fails open: entries list, every attribute read
// returns ENOENT, and the result is indistinguishable from no tree at all.
const (
	// PCIDevicesRelPath is the flat lookup directory of BDF symlinks.
	PCIDevicesRelPath = "sys/bus/pci/devices"
	// SysDevicesRelPath is the hierarchy those symlinks point into.
	SysDevicesRelPath = "sys/devices"
)

// Options controls a single rendering pass.
type Options struct {
	// Topology is the resolved layout to render. Nil or empty renders an
	// empty tree rather than leaving the previous one served.
	Topology *PCIeTopology

	// Identities carries the per-device PCI identity (device_id /
	// subsystem_id) keyed by lowercased BDF. A BDF present in the topology
	// but absent here still gets attribute files with the NVIDIA vendor
	// default, so lspci never fatals on a missing `vendor`.
	Identities map[string]PCI

	// Output is the fake-root directory. The renderer writes under
	// <Output>/sys/... — Output itself is created if missing. Required when
	// Topology is non-empty; otherwise Render returns an error. An empty
	// Topology empties the tree under Output rather than leaving it alone.
	Output string
}

// Render writes the entire tree. It is idempotent and converging: existing
// directories are reused, existing files are truncated and rewritten, existing
// symlinks are removed and recreated so a stale relative target does not linger,
// and entries the new topology no longer declares are pruned.
func Render(o Options) error {
	empty := o.Topology == nil || len(o.Topology.RootComplexes) == 0
	if o.Output == "" {
		if empty {
			return nil
		}
		return errors.New("pcisysfs render: Output is required")
	}

	// A profile declaring no PCI devices means an empty tree, not the previous
	// profile's: leftovers would go on being served at the kernel paths as if
	// the node still simulated those GPUs.
	if empty {
		return prune(o.Output, &PCIeTopology{})
	}

	root := o.Output
	if err := mkdirAll(root, PCIDevicesRelPath); err != nil {
		return err
	}
	if err := mkdirAll(root, SysDevicesRelPath); err != nil {
		return err
	}

	for _, rc := range o.Topology.RootComplexes {
		if err := renderRootComplex(root, rc, o.Identities); err != nil {
			return fmt.Errorf("rendering %s: %w", rc.ID, err)
		}
	}

	// Pruning last means a wanted device is never briefly absent: the window
	// holds the union of the old and new trees rather than a gap.
	return prune(root, o.Topology)
}

// Clear tears the tree down for good, emptying the two served directories
// without replacing them.
//
// The distinction matters because those directories are CDI mount sources: a
// consumer container binds their inodes when it starts and keeps them across an
// agent restart, so removing and re-rendering leaves it reading an empty tree
// until something recreates the pod. Everything below them goes, DMI included,
// which is what separates this from rendering an empty topology.
func Clear(root string) error {
	return errors.Join(
		prune(root, &PCIeTopology{}),
		// prune spares what the renderer does not own; a teardown owns all of it.
		pruneDir(filepath.Join(root, SysDevicesRelPath), func(string) bool { return false }),
	)
}

// safeName reports whether name can be joined under Output as a single
// directory. Every root-complex ID and BDF becomes a path component, so one
// carrying a separator or a parent reference writes outside the tree. Callers
// are expected to have validated their input; this is the backstop that keeps a
// hand-authored bus_id from reaching the host filesystem.
func safeName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`)
}

// prune removes entries the topology no longer declares. Rendering alone is
// additive, so a device set that shrinks or is re-addressed would otherwise
// leave orphans that lspci keeps enumerating and NVML no longer reports.
func prune(root string, topo *PCIeTopology) error {
	perRoot := make(map[string]map[string]bool, len(topo.RootComplexes))
	allBDFs := make(map[string]bool)

	for _, rc := range topo.RootComplexes {
		devs := make(map[string]bool, len(rc.Devices))
		for _, bdf := range rc.Devices {
			bdfLower := strings.ToLower(bdf)
			devs[bdfLower] = true
			allBDFs[bdfLower] = true
		}
		perRoot[rc.ID] = devs
	}

	errs := []error{
		// The flat lookup directory holds only BDF symlinks.
		pruneDir(filepath.Join(root, PCIDevicesRelPath),
			func(name string) bool { return allBDFs[name] }),
	}

	devicesDir := filepath.Join(root, SysDevicesRelPath)
	entries, err := os.ReadDir(devicesDir)
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("read %s: %w", devicesDir, err))
	}

	for _, e := range entries {
		// libpcisysfs rewrites only /sys/devices/pci*, so anything without that
		// prefix belongs to something other than this renderer.
		if !strings.HasPrefix(e.Name(), "pci") {
			continue
		}

		rcDir := filepath.Join(devicesDir, e.Name())
		devs, wanted := perRoot[e.Name()]
		if !wanted {
			if err := os.RemoveAll(rcDir); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", rcDir, err))
			}
			continue
		}

		// A rendered root complex contains device directories and nothing else.
		errs = append(errs, pruneDir(rcDir, func(name string) bool { return devs[name] }))
	}

	return errors.Join(errs...)
}

// pruneDir removes every entry of dir that keep rejects. A missing dir is not an
// error: nothing has been rendered there yet.
func pruneDir(dir string, keep func(string) bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}

	var errs []error
	for _, e := range entries {
		if keep(e.Name()) {
			continue
		}

		p := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

func renderRootComplex(root string, rc RootComplex, ids map[string]PCI) error {
	if !safeName(rc.ID) {
		return fmt.Errorf("root complex id %q is not a path component", rc.ID)
	}

	rcDir := filepath.Join(SysDevicesRelPath, rc.ID)
	if err := mkdirAll(root, rcDir); err != nil {
		return err
	}

	for _, bdf := range rc.Devices {
		if !safeName(bdf) {
			return fmt.Errorf("device %q is not a path component", bdf)
		}

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
		linkPath := filepath.Join(root, PCIDevicesRelPath, bdfLC)
		linkTarget := filepath.Join("..", "..", "..", "devices", rc.ID, bdfLC)
		if err := replaceSymlink(linkPath, linkTarget); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w",
				filepath.Join(PCIDevicesRelPath, bdfLC), linkTarget, err)
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
