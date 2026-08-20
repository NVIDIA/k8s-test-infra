// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gpudriver implements the GPU driver footprint simulator:
// chardevs, NVML/CUDA shims, nvidia-smi, procfs entries, engine config,
// and the /run/nvidia/driver GPU-Operator compatibility symlink.
package gpudriver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const name = "gpudriver"

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Applier   = (*Simulator)(nil)
)

// Simulator implements agent.Simulator and agent.Applier.
type Simulator struct {
	ready atomic.Bool
}

// New returns a gpudriver Simulator.
func New() *Simulator { return &Simulator{} }

func (s *Simulator) Name() string { return name }
func (s *Simulator) Ready() bool  { return s.ready.Load() }

// Stage materializes the GPU driver footprint under h.Root/driver/.
// All surfaces run in parallel; a failure in any one aborts the rest.
func (s *Simulator) Stage(ctx context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	driverRoot := filepath.Join(h.Root, "driver")

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return stageCharDevs(gctx, driverRoot, state) })
	g.Go(func() error { return stageNVMLShim(gctx, driverRoot, state.Software) })
	g.Go(func() error { return stageCUDAShim(gctx, driverRoot, state.Software) })
	g.Go(func() error { return stageNvidiaSMI(gctx, driverRoot, state.Software) })
	g.Go(func() error { return writeProcVersion(gctx, driverRoot, state.Software) })
	g.Go(func() error { return writeProcParams(gctx, driverRoot) })
	g.Go(func() error { return writeEngineConfig(gctx, h, state) })

	if err := g.Wait(); err != nil {
		return err
	}
	s.ready.Store(true)
	return nil
}

// Discard removes the driver tree and engine config written by Stage.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	driverRoot := filepath.Join(h.Root, "driver")
	if err := os.RemoveAll(driverRoot); err != nil {
		return fmt.Errorf("remove %s: %w", driverRoot, err)
	}
	configYAML := filepath.Join(h.Root, "config", "config.yaml")
	if err := os.Remove(configYAML); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", configYAML, err)
	}
	return nil
}

// Apply creates the GPU-Operator compatibility symlink at /run/nvidia/driver.
func (s *Simulator) Apply(_ context.Context, h *host.Host, _ *agent.State) error {
	return h.Symlink("/var/lib/nvml-mock/driver", filepath.Join(h.Run, "nvidia/driver"))
}

// Revoke removes the /run/nvidia/driver symlink.
func (s *Simulator) Revoke(_ context.Context, h *host.Host) error {
	return h.Remove(filepath.Join(h.Run, "nvidia/driver"))
}
