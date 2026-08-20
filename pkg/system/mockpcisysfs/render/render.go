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
// .../vendor". It is still *not* a full sysfs simulation — resource ranges,
// capabilities, and driver bindings are out of scope.
package render

import (
	"encoding/binary"
	"errors"
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
	// A non-nil Topology requires a non-empty Output; otherwise Render
	// returns an error. When Topology is nil or has no root complexes there
	// is nothing to write, and Render instead empties whatever a previous
	// profile left under Output and drops the completion marker — so a caller
	// can invoke the renderer unconditionally without leaving a tree that
	// describes the wrong profile. With Output empty too, Render does nothing.
	Output string

	// DMISource is the directory holding the node's kernel DMI identity,
	// normally /sys/class/dmi/id. The attributes found there are mirrored
	// into the tree so that bind-mounting sys/devices over the kernel's
	// does not take the DMI directory with it — see renderDMI. Empty
	// mirrors nothing.
	DMISource string
}

// MarkerRelPath is written last, once the whole tree — topology and mirrored
// DMI attributes alike — is on disk. Consumers that bind-mount the tree onto
// the kernel paths gate on it rather than on the directories they mount:
// those are created at the start of a render, so their presence says nothing
// about whether the render finished, and serving a half-rendered tree fails
// container creation on a bind target that is not there yet.
//
// It sits outside both mounted subtrees, so it is not visible to a container
// the tree is served to.
const MarkerRelPath = "sys/.rendered"

// PCIDevicesRelPath and SysDevicesRelPath are the two halves of the tree,
// relative to Options.Output. They are exported for the consumers that
// bind-mount them onto the kernel paths, so the layout has one definition
// rather than a copy per consumer.
const (
	PCIDevicesRelPath = "sys/bus/pci/devices"
	SysDevicesRelPath = "sys/devices"
)

// Render writes the entire tree, replacing whatever a previous render left
// behind, and marks it complete with MarkerRelPath. Within a render existing
// files are truncated and rewritten, and existing symlinks are removed and
// recreated so a stale relative target cannot linger.
func Render(o Options) error {
	if !o.hasTopology() {
		// Nothing to render — the caller passed a profile with no declared
		// topology and no devices, which setup.sh does unconditionally. A tree
		// left here by a previous profile would still describe the node, so it
		// is emptied rather than kept.
		if o.Output == "" {
			return nil
		}
		return pruneTree(o.Output)
	}
	if o.Output == "" {
		return errors.New("pcisysfs render: Output is required")
	}

	if err := pruneTree(o.Output); err != nil {
		return err
	}
	if err := renderTopology(o); err != nil {
		return err
	}
	if err := renderDMI(o.Output, o.DMISource); err != nil {
		return err
	}
	return writeFile(o.Output, MarkerRelPath, "")
}

func (o Options) hasTopology() bool {
	return o.Topology != nil && len(o.Topology.RootComplexes) > 0
}

// pruneTree drops the devices a previous render left behind. Rendering only
// ever added entries, so without this a re-profiled node keeps both profiles'
// devices (an a100 and an h100 share no BDFs) and consumers see their union
// mounted at /sys/bus/pci/devices — more GPUs than the node simulates, some
// under a root complex no profile declares.
//
// What survives is deliberate: the two directories consumers bind-mount, and
// the DMI directory holding the targets kind's createContainer hook needs. A
// container created while a render is in flight then still finds every mount's
// source and every target in place — it may see fewer devices than the profile
// declares, but it starts. Consumers served through CDI have no way to wait
// for MarkerRelPath, since the runtime applies the spec's mounts unconditionally.
func pruneTree(root string) error {
	if err := os.RemoveAll(filepath.Join(root, MarkerRelPath)); err != nil {
		return fmt.Errorf("clear %s: %w", MarkerRelPath, err)
	}
	if err := removeEntries(filepath.Join(root, PCIDevicesRelPath), ""); err != nil {
		return err
	}
	return removeEntries(filepath.Join(root, SysDevicesRelPath), dmiVirtualDirName)
}

// removeEntries empties dir, keeping the entry named keep (if any). A missing
// dir is not an error: there is nothing to prune on a first render.
func removeEntries(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("clear %s: %w", filepath.Join(dir, entry.Name()), err)
		}
	}
	return nil
}

func renderTopology(o Options) error {
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
	return nil
}

// dmiIDDir is where the kernel materializes the SMBIOS identity; the
// familiar /sys/class/dmi/id path is only a symlink into it.
// dmiVirtualDirName names its top-level directory under sys/devices, which
// pruneTree keeps so the mount targets inside it never go missing.
const (
	dmiVirtualDirName = "virtual"
	dmiIDDir          = SysDevicesRelPath + "/" + dmiVirtualDirName + "/dmi/id"
)

// dmiMirroredAttrs are the DMI attributes kind's mount-product-files.sh
// createContainer hook bind-mounts the node's copies onto, for every
// container on the node. Each has to exist in the tree as a mount target;
// byValue says whether the node's value travels with it.
var dmiMirroredAttrs = []struct {
	name    string
	byValue bool
}{
	// The node's machine type, which consumers do read: GFD's default
	// machine-type file resolves here.
	{name: "product_name", byValue: true},
	// A node identifier the kernel deliberately exposes 0400 to root alone.
	// Only its existence matters, since kind mounts the node's own copy over
	// it, so the tree carries an empty stand-in rather than republishing the
	// value world-readable into every served container.
	{name: "product_uuid"},
}

// renderDMI mirrors the node's DMI identity into the tree. Serving the tree
// means bind-mounting it over /sys/devices, which also replaces
// virtual/dmi/id — the directory /sys/class/dmi/id resolves into. Any
// attribute missing from the replacement is a bind-mount target that no
// longer exists, and mount(8) cannot create one on a read-only sysfs, so
// kind's hook fails and every injected container fails to start.
//
// Mirroring rather than mocking keeps the node's identity intact: kind
// already reports its own ("kind" as the product name, a random UUID), and
// overriding that is a separate concern with its own consumers.
func renderDMI(root, source string) error {
	if source == "" {
		return nil
	}
	for _, attr := range dmiMirroredAttrs {
		src := filepath.Join(source, attr.name)
		if _, err := os.Stat(src); err != nil {
			// The kernel exposes no such attribute, so nothing bind-mounts
			// it either and a stand-in would only invent an identity.
			continue
		}
		var contents []byte
		if attr.byValue {
			// A read failure still has to leave the file behind: the target
			// matters more than the value, and mount(8) cannot create one.
			if value, err := os.ReadFile(src); err == nil {
				contents = value
			}
		}
		if err := writeFile(root, filepath.Join(dmiIDDir, attr.name), string(contents)); err != nil {
			return err
		}
	}
	return nil
}

func renderRootComplex(root string, rc config.RootComplex, ids map[string]config.PCI) error {
	rcDir := filepath.Join(SysDevicesRelPath, rc.ID)
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
