// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gpudriver implements the GPU driver footprint simulator:
// chardevs, NVML/CUDA shims, nvidia-smi, procfs entries, engine config, and a
// bind mount of that tree onto /run/nvidia/driver, the path GPU Operator's
// own operands default to.
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
	"github.com/NVIDIA/k8s-test-infra/internal/kmod"
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

// Ready reports whether the driver footprint and its bind mount exist.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the GPU driver footprint under host.Root/driver/.
// All surfaces run in parallel; a failure in any one cancels the rest via gctx.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return stageCharDevs(gctx, s.host, state) })
	g.Go(func() error { return stageNVMLShim(gctx, s.host, state) })
	g.Go(func() error { return stageCUDAShim(gctx, s.host, state) })
	g.Go(func() error { return stageNvidiaSMI(gctx, s.host, state) })
	g.Go(func() error { return writeProcFS(gctx, s.host, state) })
	g.Go(func() error { return writeEngineConfig(gctx, s.host, state) })
	g.Go(func() error { return writeMachineType(gctx, s.host, state) })
	g.Go(func() error { return writeKernelModules(gctx, s.host, state) })

	if err := g.Wait(); err != nil {
		return err
	}

	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

// stagedPaths lists the paths that Discard removes, in removal order (leaves
// first). sys/module is not in the list. kmod.Clear empties it in place.
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
	machineTypeRel,
	"config/config.yaml",
	kmod.ProcModulesRelPath,
	kmod.LsmodRelPath,
}

// Discard removes the paths Stage writes and empties sys/module in place. Every
// path is exclusively owned by gpudriver, so removing absent or partially staged
// paths is safe.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	var errs []error

	for _, rel := range stagedPaths {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	if err := kmod.Clear(s.host.Root); err != nil {
		errs = append(errs, fmt.Errorf("clear kernel modules: %w", err))
	}

	return errors.Join(errs...)
}

// Apply bind-mounts the staged driver tree onto /run/nvidia/driver — the
// same mechanism the real NVIDIA driver container uses — so a consumer that
// already bind-mounted the path isn't orphaned, and exec resolves against
// the mount source rather than /run's own (often noexec) flags.
func (s *Simulator) Apply(_ context.Context, _ *agent.State) error {
	zap.L().Info("applying simulator", zap.String("simulator", name))
	s.ready.Store(false)

	driverRoot := s.host.RunPath("nvidia/driver")
	if err := fsutil.BindMount(s.host.RootPath("driver"), driverRoot); err != nil {
		return fmt.Errorf("bind mount %s: %w", driverRoot, err)
	}

	s.ready.Store(true)
	return nil
}

// Revoke unmounts /run/nvidia/driver, leaving the directory itself in place.
func (s *Simulator) Revoke(_ context.Context) error {
	zap.L().Info("revoking simulator", zap.String("simulator", name))
	s.ready.Store(false)

	if err := fsutil.Unmount(s.host.RunPath("nvidia/driver")); err != nil {
		return fmt.Errorf("unmount %s: %w", s.host.RunPath("nvidia/driver"), err)
	}

	return nil
}
