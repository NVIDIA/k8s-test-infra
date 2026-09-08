// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

const (
	// kernelDMIRelPath is the kernel's DMI directory relative to /sys;
	// /sys/class/dmi/id is a symlink to it.
	kernelDMIRelPath = "devices/virtual/dmi/id"
	// mockDMIRelPath is the same directory inside the rendered tree.
	mockDMIRelPath = pcisysfs.SysDevicesRelPath + "/virtual/dmi/id"
)

// dmiMountTargets are the DMI attributes a container reads through
// /sys/class/dmi/id. product_uuid identifies the node and the kernel shows it
// to root alone, so it is staged empty — a target to mount over, not a value.
var dmiMountTargets = []struct {
	name     string
	byValue  bool
	fileMode os.FileMode
}{
	{name: "product_name", byValue: true, fileMode: 0o444},
	{name: "product_uuid", byValue: false, fileMode: 0o400},
}

// stageDMI reproduces the node's DMI attributes inside the rendered tree.
// Serving the tree at /sys/devices replaces the directory /sys/class/dmi/id
// resolves into, so on any cluster a served container would otherwise read
// ENOENT where the node has values.
//
// Under kind it is worse than a missing value: the node image bind-mounts its
// own product files into every container, mount(8) cannot create a target on a
// read-only sysfs, and every served pod dies on "mount point does not exist".
//
// Deliberately not a machine-type mock — that is writeMachineType's job (#681).
func stageDMI(h *host.Host) error {
	kernelDir := filepath.Join(h.Sys, kernelDMIRelPath)
	if _, err := os.Stat(kernelDir); err != nil {
		// No kernel DMI means no hook to satisfy either: both test the host.
		return nil
	}

	mockDir := filepath.Join(h.Root, mockDMIRelPath)
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		return err
	}

	for _, attr := range dmiMountTargets {
		var value []byte
		if attr.byValue {
			// Unreadable is not fatal: a mount target need not carry a value.
			value, _ = os.ReadFile(filepath.Join(kernelDir, attr.name))
		}
		if err := fsutil.Write(filepath.Join(mockDir, attr.name), value, attr.fileMode); err != nil {
			return fmt.Errorf("stage dmi %s: %w", attr.name, err)
		}
	}

	return nil
}

// stageSysfs renders the PCI sysfs tree under h.Root, plus the DMI mount
// targets a container served that tree needs. A state with no PCI topology
// empties the tree and stages no DMI, since nothing is served.
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

// buildTopology maps the state's reconciled layout onto the renderer's type,
// adding the Mellanox NICs the simulated HCAs hang off. Returns nil when there
// is nothing to render, which Render treats as a no-op.
func buildTopology(state *agent.State) *pcisysfs.PCIeTopology {
	rcs := state.PCITopology()
	nics := nicsFor(state)

	if len(rcs) == 0 && len(nics) == 0 {
		return nil
	}

	topo := &pcisysfs.PCIeTopology{RootComplexes: make([]pcisysfs.RootComplex, 0, len(rcs)+len(nics))}
	for _, rc := range rcs {
		topo.RootComplexes = append(topo.RootComplexes, pcisysfs.RootComplex{
			ID:       rc.ID,
			NUMANode: rc.NUMANode,
			Devices:  rc.DeviceBDFs,
		})
	}

	// One root complex each, mirroring how a real host puts every NIC behind
	// its own bridge, and keeping them out of the pruning that the declared
	// root complexes' device lists drive.
	for _, nic := range nics {
		topo.RootComplexes = append(topo.RootComplexes, pcisysfs.RootComplex{
			ID:      nicRootComplexID(nic.BusID),
			Devices: []string{nic.BusID},
		})
	}

	return topo
}

// nicRootComplexID names the bridge a NIC hangs off, after the kernel's
// pciDDDD:BB naming. The prefix matters: the renderer prunes only entries
// starting with "pci", leaving anything else alone as another writer's.
func nicRootComplexID(bdf string) string {
	domain, bus, _ := strings.Cut(bdf, ":")
	bus, _, _ = strings.Cut(bus, ":")

	return "pci" + domain + ":" + bus
}

// nicsFor derives the NICs, logging rather than failing when they collide with
// a declared address: a reconcile that drops the whole PCI tree would take the
// GPUs with it, which is worse than an HCA nothing discovers.
func nicsFor(state *agent.State) []pcisysfs.PCI {
	if state == nil {
		return nil
	}

	declared := make(map[string]struct{}, len(state.Devices))
	for _, d := range state.Devices {
		if d.PCIBusID != "" {
			declared[strings.ToLower(d.PCIBusID)] = struct{}{}
		}
	}

	nics, err := mellanoxNICs(state.NodeShape.Network, declared)
	if err != nil {
		slog.Error("rendering no Mellanox NICs; the simulated HCAs will not be discoverable",
			"simulator", name, "err", err)

		return nil
	}

	return nics
}

// Where the derived NICs live. A high bus keeps them clear of the low ones
// profiles give their GPUs, and one function per bus mirrors a real single-port
// adapter.
const (
	nicFirstBus = 0xc0
	nicLastBus  = 0xff
)

// mlx5Driver is the module a real ConnectX binds to. Consumers read the driver
// link to decide a device is claimed, so the name has to be the real one.
const mlx5Driver = "mlx5_core"

// connectX7DeviceID is the PCI device ID of a ConnectX-7, matching the HCA type
// the profiles declare by default.
const connectX7DeviceID = 0x1021

// mellanoxNICs derives one NIC per simulated HCA. RDMA consumers discover an
// HCA only by walking the PCI device it hangs off, so the NIC is what makes a
// simulated HCA visible at all; its addresses are derived rather than
// configured so the two can never disagree about how many there are.
func mellanoxNICs(net agent.NetworkShape, declared map[string]struct{}) ([]pcisysfs.PCI, error) {
	if !net.IBEnabled || net.HCACount <= 0 {
		return nil, nil
	}

	if net.HCACount > nicLastBus-nicFirstBus+1 {
		return nil, fmt.Errorf("hca_count=%d exceeds the %d PCI addresses reserved for mock NICs",
			net.HCACount, nicLastBus-nicFirstBus+1)
	}

	nics := make([]pcisysfs.PCI, 0, net.HCACount)

	for i := range net.HCACount {
		bdf := fmt.Sprintf("0000:%02x:00.0", nicFirstBus+i)
		if _, taken := declared[bdf]; taken {
			return nil, fmt.Errorf("mock NIC address %s is already declared by another device", bdf)
		}

		nics = append(nics, pcisysfs.PCI{
			BusID: bdf,
			// Packed as the renderer unpacks it: device in the high half,
			// vendor in the low.
			DeviceID: connectX7DeviceID<<16 | pcisysfs.MellanoxVendorID,
			Class:    pcisysfs.ClassInfiniband,
			Driver:   mlx5Driver,
			Netdev:   fmt.Sprintf("%s%d", net.NetdevPrefix, i),
			IBDevice: fmt.Sprintf("mlx5_%d", i),
		})
	}

	return nics, nil
}

// buildIdentities maps each device's lowercased BDF to its PCI identity for
// the renderer's attribute files (vendor, device, class, config space).
func buildIdentities(state *agent.State) map[string]pcisysfs.PCI {
	ids := make(map[string]pcisysfs.PCI, len(state.Devices))

	// The NICs carry their own identity: vendor, driver and the netdev and RDMA
	// directories a consumer walks from the device to the HCA behind it.
	for _, nic := range nicsFor(state) {
		ids[nic.BusID] = nic
	}

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
