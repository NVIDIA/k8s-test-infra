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
	"path/filepath"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const name = "gpudriver"

// The GPU-Operator compatibility symlink and the driver root it points at.
// This resolves to the node's real /run/nvidia/driver, a path the GPU Operator's
// driver container also owns.
const (
	driverLinkRel    = "nvidia/driver"
	driverLinkTarget = "/var/lib/nvml-mock/driver"
)

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

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the driver footprint and its published symlink exist.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the GPU driver footprint under h.Root/driver/.
// All surfaces run in parallel; a failure in any one cancels the rest via gctx.
func (s *Simulator) Stage(ctx context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return stageCharDevs(gctx, h, state) })
	g.Go(func() error { return stageNVMLShim(gctx, h, state) })
	g.Go(func() error { return stageCUDAShim(gctx, h, state) })
	g.Go(func() error { return stageNvidiaSMI(gctx, h, state) })
	g.Go(func() error { return writeProcFS(gctx, h, state) })
	g.Go(func() error { return writeEngineConfig(gctx, h, state) })
	g.Go(func() error { return writeMachineType(gctx, h, state) })

	if err := g.Wait(); err != nil {
		return err
	}

	zap.L().Info("simulator staged", zap.String("simulator", name))
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
	machineTypeRel,
	"config/config.yaml",
}

// Discard removes only the paths Stage writes. Every path is exclusively owned
// by gpudriver, so removing absent or partially staged paths is safe.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	var errs []error

	for _, rel := range stagedPaths {
		p := filepath.Join(h.Root, rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

// Apply creates the GPU-Operator compatibility symlink at /run/nvidia/driver,
// replacing whatever is already there.
func (s *Simulator) Apply(_ context.Context, h *host.Host, _ *agent.State) error {
	zap.L().Info("applying simulator", zap.String("simulator", name))
	s.ready.Store(false)

	driverLink := h.RunPath(driverLinkRel)

	// Called for the warning it emits: displacing another owner's driver root is
	// worth a log line even though we go on to do it.
	if _, err := ownsDriverLink(driverLink); err != nil {
		return err
	}

	if err := fsutil.Symlink(driverLinkTarget, driverLink); err != nil {
		return err
	}

	s.ready.Store(true)
	return nil
}

// Revoke removes the /run/nvidia/driver symlink. Anything else at that path
// belongs to another owner of the node's /run/nvidia and is left alone.
func (s *Simulator) Revoke(_ context.Context, h *host.Host) error {
	zap.L().Info("revoking simulator", zap.String("simulator", name))
	s.ready.Store(false)

	link := h.RunPath(driverLinkRel)

	ours, err := ownsDriverLink(link)

	if err != nil || !ours {
		return err
	}

	return fsutil.Remove(link)
}

// ownsDriverLink reports whether link is the symlink Apply created. Absent, not
// a symlink, or pointing elsewhere all mean it is not ours.
func ownsDriverLink(link string) (bool, error) {
	fi, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("lstat %s: %w", link, err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		zap.L().Warn("driver root is not our symlink",
			zap.String("path", link), zap.String("type", fi.Mode().Type().String()))

		return false, nil
	}

	target, err := os.Readlink(link)
	if err != nil {
		return false, fmt.Errorf("readlink %s: %w", link, err)
	}

	if target != driverLinkTarget {
		zap.L().Warn("driver symlink points elsewhere",
			zap.String("path", link), zap.String("target", target))

		return false, nil
	}

	return true, nil
}
