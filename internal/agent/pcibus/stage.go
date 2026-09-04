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

// buildTopology maps the state's reconciled layout onto the renderer's type.
// Returns nil when there is nothing to render, which Render treats as a no-op.
func buildTopology(state *agent.State) *pcisysfs.PCIeTopology {
	rcs := state.PCITopology()
	if len(rcs) == 0 {
		return nil
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
