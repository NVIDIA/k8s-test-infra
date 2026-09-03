// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gpudriver implements the GPU driver footprint simulator:
// chardevs, NVML/CUDA shims, nvidia-smi, procfs entries, engine config,
// and the /run/nvidia/driver GPU-Operator compatibility symlink.
package gpudriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

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
	host  *host.Host
	ready atomic.Bool
}

// New returns a gpudriver Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the GPU driver footprint under host.Root/driver/.
// All surfaces run in parallel; a failure in any one cancels the rest via gctx.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Debug("staging simulator", zap.String("simulator", name))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return stageCharDevs(gctx, s.host, state) })
	g.Go(func() error { return stageNVMLShim(gctx, s.host, state) })
	g.Go(func() error { return stageCUDAShim(gctx, s.host, state) })
	g.Go(func() error { return stageNvidiaSMI(gctx, s.host, state) })
	g.Go(func() error { return writeProcFS(gctx, s.host, state) })
	g.Go(func() error { return writeEngineConfig(gctx, s.host, state) })

	if err := g.Wait(); err != nil {
		return err
	}
	s.ready.Store(true)
	zap.L().Debug("simulator staged", zap.String("simulator", name))
	return nil
}

// stagedPaths lists exactly the paths Stage writes, in removal order (leaves first).
// RemoveAll on the whole driver/ tree is intentionally avoided: the ib and pcibus
// simulators stage tools, libibverbs.d and preload shims there, and those must
// survive Discard.
var stagedPaths = []string{
	"driver/dev",
	"driver/usr/lib64",
	"driver/usr/bin/nvidia-smi",
	"driver/usr/bin/nvidia-smi.sh",
	"driver/proc/driver/nvidia",
	"driver/config/config.yaml",
	"config/config.yaml",
}

// Discard removes only the paths Stage wrote. It is a no-op when Stage never
// completed successfully, so it does not disturb a partially initialised tree.
func (s *Simulator) Discard(_ context.Context) error {
	if !s.ready.Load() {
		return nil
	}
	zap.L().Debug("discarding simulator", zap.String("simulator", name))
	var errs []error
	for _, rel := range stagedPaths {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// Apply creates the GPU-Operator compatibility symlink at /run/nvidia/driver.
func (s *Simulator) Apply(_ context.Context, _ *agent.State) error {
	return fsutil.Symlink("/var/lib/nvml-mock/driver", s.host.RunPath("nvidia/driver"))
}

// Revoke removes the /run/nvidia/driver symlink.
func (s *Simulator) Revoke(_ context.Context) error {
	zap.L().Debug("revoking simulator", zap.String("simulator", name))
	return fsutil.Remove(s.host.RunPath("nvidia/driver"))
}
