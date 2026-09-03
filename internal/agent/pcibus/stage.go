// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

// defaultRootComplexID is the host bridge a flat fallback topology hangs every
// device off, matching what a single-socket profile declares explicitly.
const defaultRootComplexID = "pci0000:00"

// stageSysfs renders the PCI sysfs tree under h.Root.
// When the state carries no root complexes Render is a no-op.
func stageSysfs(h *host.Host, state *agent.State) error {
	return pcisysfs.Render(pcisysfs.Options{
		Topology:   buildTopology(state),
		Identities: buildIdentities(state),
		Output:     h.Root,
	})
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
		zap.L().Debug("no libpcisysfs shim in image; skipping PCI shim staging")
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
