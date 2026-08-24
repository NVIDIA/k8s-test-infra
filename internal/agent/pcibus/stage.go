// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
)

// stageSysfs renders the PCI sysfs tree under h.Root.
// When the state carries no root complexes render.Render is a no-op.
func stageSysfs(h *host.Host, state *agent.State) error {
	return render.Render(render.Options{
		Topology:   buildTopology(state),
		Identities: buildIdentities(state),
		Output:     h.Root,
	})
}

// stagePCIShim copies libpcimocksys.so* from /usr/local/lib into the driver
// lib directory so lspci inside a workload can be LD_PRELOAD-ed by the NRI
// plugin. Non-fatal when the shim is not built into the container image.
func stagePCIShim(h *host.Host) error {
	matches, _ := filepath.Glob("/usr/local/lib/libpcimocksys.so*")

	if len(matches) == 0 {
		return nil
	}

	libDir := filepath.Join(h.Root, "driver/usr/local/lib")

	if err := h.MkdirAll(libDir, 0o755); err != nil {
		return err
	}

	for _, src := range matches {
		dst := filepath.Join(libDir, filepath.Base(src))
		if err := h.CopyFile(src, dst, 0o755); err != nil {
			return fmt.Errorf("stage %s: %w", filepath.Base(src), err)
		}
	}

	return nil
}

// buildTopology converts the agent's PCIeTopology to the render package's type.
// Returns nil when the state carries no root complexes (render treats nil as no-op).
func buildTopology(state *agent.State) *config.PCIeTopology {
	rcs := state.NodeShape.Topology.RootComplexes

	if len(rcs) == 0 {
		return nil
	}

	topo := &config.PCIeTopology{RootComplexes: make([]config.RootComplex, 0, len(rcs))}

	for _, rc := range rcs {
		topo.RootComplexes = append(topo.RootComplexes, config.RootComplex{
			ID:       rc.ID,
			NUMANode: rc.NUMANode,
			Devices:  rc.DeviceBDFs,
		})
	}

	return topo
}

// buildIdentities maps each device's lowercased BDF to its PCI identity for
// the renderer's attribute files (vendor, device, class, config space).
func buildIdentities(state *agent.State) map[string]config.PCI {
	ids := make(map[string]config.PCI, len(state.Devices))

	for _, d := range state.Devices {
		if d.PCIBusID == "" {
			continue
		}

		ids[strings.ToLower(d.PCIBusID)] = config.PCI{
			BusID:    d.PCIBusID,
			DeviceID: d.PCIDeviceID,
		}
	}

	return ids
}
