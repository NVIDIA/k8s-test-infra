// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package pcibus implements the PCI bus simulator: it renders a fake
// /sys/bus/pci/devices tree and stages libpcimocksys.so so that lspci and
// topology-aware schedulers see mock GPU BDFs. It also writes the NFD
// local-source feature file so NFD can label the node with
// feature.node.kubernetes.io/pci-10de.present=true.
package pcibus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
)

const (
	name           = "pcibus"
	nfdFeatureFile = "kubernetes/node-feature-discovery/features.d/nvml-mock.features"
	nfdContent     = "pci-10de.present=true\n"
)

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Applier   = (*Simulator)(nil)
)

// Simulator implements agent.Simulator and agent.Applier.
type Simulator struct {
	ready atomic.Bool
}

// New returns a pcibus Simulator.
func New() *Simulator { return &Simulator{} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage renders the PCI sysfs tree under h.Root and stages libpcimocksys.so.
// When the state carries no topology the render is a no-op.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	topo := buildTopology(state)
	if err := render.Render(render.Options{
		Topology:   topo,
		Identities: buildIdentities(state),
		Output:     h.Root,
	}); err != nil {
		return fmt.Errorf("render pci sysfs: %w", err)
	}

	if err := stagePCIShim(h); err != nil {
		return err
	}

	s.ready.Store(true)
	return nil
}

// Discard removes the rendered PCI sysfs tree and staged shim. It is a no-op
// when Stage never completed successfully.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	if !s.ready.Load() {
		return nil
	}

	var errs []error
	// Remove the entire sys/ subtree; pcibus is its only writer.
	sysRoot := filepath.Join(h.Root, "sys")
	if err := os.RemoveAll(sysRoot); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("remove %s: %w", sysRoot, err))
	}

	// Remove staged shim files.
	shimGlob := filepath.Join(h.Root, "driver/usr/local/lib/libpcimocksys.so*")
	matches, _ := filepath.Glob(shimGlob)
	for _, p := range matches {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

// Apply writes the NFD local-source feature file so NFD can derive
// feature.node.kubernetes.io/pci-10de.present=true from it.
func (s *Simulator) Apply(_ context.Context, h *host.Host, _ *agent.State) error {
	return h.WriteFile(filepath.Join(h.Etc, nfdFeatureFile), []byte(nfdContent), 0o644)
}

// Revoke removes the NFD feature file.
func (s *Simulator) Revoke(_ context.Context, h *host.Host) error {
	return h.Remove(filepath.Join(h.Etc, nfdFeatureFile))
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
