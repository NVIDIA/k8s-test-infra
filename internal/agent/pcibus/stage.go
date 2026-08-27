// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

// defaultRootComplexID is the host bridge a flat fallback topology hangs every
// device off, matching what a single-socket profile declares explicitly.
const defaultRootComplexID = "pci0000:00"

const (
	// kernelDMIRelPath is where the kernel keeps DMI attributes, relative to
	// /sys. /sys/class/dmi/id is a symlink to it.
	kernelDMIRelPath = "devices/virtual/dmi/id"
	// mockDMIRelPath is the same directory inside the rendered tree, relative
	// to the overlay root.
	mockDMIRelPath = pcisysfs.SysDevicesRelPath + "/virtual/dmi/id"
)

// dmiMountTargets are the attributes kind's mount-product-files.sh hook
// bind-mounts into every container. Only product_name travels by value:
// product_uuid identifies the node and the kernel exposes it to root alone, so
// it is staged as an empty file that exists purely to be mounted over.
var dmiMountTargets = []struct {
	name     string
	byValue  bool
	fileMode os.FileMode
}{
	{name: "product_name", byValue: true, fileMode: 0o444},
	{name: "product_uuid", byValue: false, fileMode: 0o400},
}

// stageDMI reproduces, inside the rendered tree, the DMI attributes kind's
// createContainer hook bind-mounts into every container.
//
// This is mount-target compatibility, not a machine-type mock (#681). Serving
// the tree at /sys/devices replaces the directory /sys/class/dmi/id resolves
// into, and mount(8) cannot create a target on a read-only sysfs — so without
// these files every served pod dies with
//
//	mount: .../sys/class/dmi/id/product_uuid: mount point does not exist
//
// on Linux, while passing on Docker Desktop, whose linuxkit VM exposes no DMI
// and where kind's hook therefore does not fire either.
func stageDMI(h *host.Host) error {
	kernelDir := filepath.Join(h.Sys, kernelDMIRelPath)
	if _, err := os.Stat(kernelDir); err != nil {
		// No DMI on this kernel means no /sys/class/dmi to resolve through and
		// no hook to satisfy, so there is nothing to stand in for.
		return nil
	}

	mockDir := filepath.Join(h.Root, mockDMIRelPath)
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		return err
	}

	for _, attr := range dmiMountTargets {
		var value []byte
		if attr.byValue {
			// Unreadable is not fatal: the file's job as a mount target does
			// not depend on carrying the node's value.
			value, _ = os.ReadFile(filepath.Join(kernelDir, attr.name))
		}
		if err := fsutil.Write(filepath.Join(mockDir, attr.name), value, attr.fileMode); err != nil {
			return fmt.Errorf("stage dmi %s: %w", attr.name, err)
		}
	}

	return nil
}

// stageSysfs renders the PCI sysfs tree under h.Root, together with the DMI
// mount targets a container served that tree needs. When the state declares no
// PCI topology the tree is emptied instead and no DMI is staged, since nothing
// is served.
func stageSysfs(h *host.Host, state *agent.State) error {
	if err := pcisysfs.Render(pcisysfs.Options{
		Topology:   buildTopology(state),
		Identities: buildIdentities(state),
		Output:     h.Root,
	}); err != nil {
		return err
	}

	if !state.HasPCITopology() {
		return nil
	}

	return stageDMI(h)
}

// shimGlob locates the shim in the container image. A package var so tests can
// exercise both branches without depending on what the host has installed.
var shimGlob = "/usr/local/lib/libpcisysfs.so*"

// stagePCIShim copies libpcisysfs.so* from /usr/local/lib into the driver
// lib directory so lspci inside a workload can be LD_PRELOAD-ed by the NRI
// plugin. Non-fatal when the shim is not built into the container image.
func stagePCIShim(h *host.Host) error {
	matches, _ := filepath.Glob(shimGlob)

	if len(matches) == 0 {
		return nil
	}

	libDir := filepath.Join(h.Root, "driver/usr/local/lib")

	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return err
	}

	for _, src := range matches {
		dst := filepath.Join(libDir, filepath.Base(src))
		if err := fsutil.Copy(src, dst, 0o755); err != nil {
			return fmt.Errorf("stage %s: %w", filepath.Base(src), err)
		}
	}

	return nil
}

// buildTopology converts the agent's PCIeTopology to the pcisysfs type, falling
// back to a flat single-root layout when the state declares no root complexes.
// Returns nil only when no device carries a BDF (Render treats nil as no-op).
func buildTopology(state *agent.State) *pcisysfs.PCIeTopology {
	rcs := state.NodeShape.Topology.RootComplexes

	if len(rcs) == 0 {
		return flatTopology(state)
	}

	topo := &pcisysfs.PCIeTopology{RootComplexes: make([]pcisysfs.RootComplex, 0, len(rcs))}

	for _, rc := range rcs {
		topo.RootComplexes = append(topo.RootComplexes, pcisysfs.RootComplex{
			ID:       rc.ID,
			NUMANode: rc.NUMANode,
			Devices:  rc.DeviceBDFs,
		})
	}

	return topo
}

// flatTopology synthesizes one root complex covering every device that declares
// a BDF. A config with devices but no pcie_topology block is a single-socket
// node, so rendering nothing at all would leave lspci with no devices to find.
func flatTopology(state *agent.State) *pcisysfs.PCIeTopology {
	rc := pcisysfs.RootComplex{ID: defaultRootComplexID}

	for _, d := range state.Devices {
		if d.PCIBusID == "" {
			continue
		}

		rc.Devices = append(rc.Devices, strings.ToLower(d.PCIBusID))
	}

	if len(rc.Devices) == 0 {
		return nil
	}

	return &pcisysfs.PCIeTopology{RootComplexes: []pcisysfs.RootComplex{rc}}
}

// buildIdentities maps each device's lowercased BDF to its PCI identity for
// the renderer's attribute files (vendor, device, class, config space).
func buildIdentities(state *agent.State) map[string]pcisysfs.PCI {
	ids := make(map[string]pcisysfs.PCI, len(state.Devices))

	for _, d := range state.Devices {
		if d.PCIBusID == "" {
			continue
		}

		ids[strings.ToLower(d.PCIBusID)] = pcisysfs.PCI{
			BusID:       d.PCIBusID,
			DeviceID:    d.PCIDeviceID,
			SubsystemID: d.PCISubsystemID,
		}
	}

	return ids
}
