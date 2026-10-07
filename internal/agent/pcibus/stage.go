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

// stageSysfs renders the PCI sysfs tree under h.Root. A state with no PCI
// topology empties the tree. Entries of sys/devices outside pci* belong to
// other simulators — the dmi simulator's copy among them — and are left alone.
func stageSysfs(h *host.Host, state *agent.State) error {
	return pcisysfs.Render(pcisysfs.Options{
		Topology:    buildTopology(state),
		Identities:  buildIdentities(state),
		OverlayRoot: h.Root,
	})
}

// shimGlob locates the shim in the container image. A package var so tests can
// exercise both branches without depending on what the host has installed.
var shimGlob = "/usr/local/lib/libmockfs.so*"

// stagePCIShim copies libmockfs.so* from /usr/local/lib into the driver
// lib directory so lspci inside a workload can be LD_PRELOAD-ed by the NRI
// plugin. Non-fatal when the shim is not built into the container image.
func stagePCIShim(h *host.Host) error {
	matches, _ := filepath.Glob(shimGlob)

	if len(matches) == 0 {
		zap.L().Debug("no libmockfs shim in image; skipping PCI shim staging")
		return nil
	}

	libDir := h.RootPath("driver/usr/local/lib")

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

// buildIdentities maps each PCI function's lowercased BDF to its identity for
// the renderer's attribute files (vendor, device, class, config space).
//
// This is the one place that knows which kind of hardware a BDF names, so it is
// where the class is decided: a GPU enumerates as a 3D controller, an NVSwitch
// as a bridge. Getting that wrong is not cosmetic — GPU Feature Discovery reads
// the class out of this tree to derive nvidia.com/gpu.mode.
func buildIdentities(state *agent.State) map[string]pcisysfs.PCI {
	ids := make(map[string]pcisysfs.PCI, len(state.Devices)+len(state.Switches))

	for _, d := range state.Devices {
		if d.PCIBusID == "" {
			continue
		}

		ids[strings.ToLower(d.PCIBusID)] = pcisysfs.PCI{
			BusID:       d.PCIBusID,
			DeviceID:    d.PCIDeviceID,
			SubsystemID: d.PCISubsystemID,
			Class:       pcisysfs.PCIClass3DController,
		}
	}

	for _, sw := range state.Switches {
		if sw.PCIBusID == "" {
			continue
		}

		ids[strings.ToLower(sw.PCIBusID)] = pcisysfs.PCI{
			BusID:       sw.PCIBusID,
			DeviceID:    sw.PCIDeviceID,
			SubsystemID: sw.PCISubsystemID,
			Class:       pcisysfs.PCIClassBridge,
		}
	}

	return ids
}
