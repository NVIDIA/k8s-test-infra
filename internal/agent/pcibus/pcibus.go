// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package pcibus implements the PCI bus simulator: it renders a fake
// /sys/bus/pci/devices tree and stages libpcisysfs.so so that lspci and
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
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
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
	host  *host.Host
	ready atomic.Bool
}

// New returns a pcibus Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the PCI surfaces and NFD feature file are published.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage renders the PCI sysfs tree under host.Root and stages libpcisysfs.so.
// When the state carries no topology the render is a no-op.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	if err := stageSysfs(s.host, state); err != nil {
		return fmt.Errorf("render pci sysfs: %w", err)
	}

	if err := stagePCIShim(s.host); err != nil {
		return err
	}

	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

// Discard empties the rendered PCI sysfs tree and removes the staged shim.
// pcibus owns both, so clearing absent or partially staged paths is safe.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	var errs []error

	// Emptied rather than removed: the CDI spec mounts these directories, and a
	// container holding one keeps the inode it started with.
	if err := pcisysfs.Clear(s.host.Root); err != nil {
		errs = append(errs, fmt.Errorf("clear pci sysfs: %w", err))
	}

	// Remove staged shim files.
	shimGlob := s.host.RootPath("driver/usr/local/lib/libpcisysfs.so*")
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
func (s *Simulator) Apply(_ context.Context, _ *agent.State) error {
	s.ready.Store(false)

	if err := fsutil.Write(s.host.EtcPath(nfdFeatureFile), []byte(nfdContent), 0o644); err != nil {
		return err
	}

	s.ready.Store(true)

	return nil
}

// Revoke removes the NFD feature file.
func (s *Simulator) Revoke(_ context.Context) error {
	zap.L().Info("revoking simulator", zap.String("simulator", name))
	s.ready.Store(false)

	return fsutil.Remove(s.host.EtcPath(nfdFeatureFile))
}
