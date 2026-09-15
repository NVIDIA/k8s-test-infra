// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gpudriver implements the GPU driver footprint simulator:
// chardevs, NVML/CUDA shims, nvidia-smi, procfs entries, engine config,
// and its mirror at /run/nvidia/driver, the path GPU Operator's own
// operands default to.
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

// Ready reports whether the driver footprint and its published mirror exist.
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

// runMirroredDirs lists the driver/ subtrees Apply mirrors into
// /run/nvidia/driver. driver/dev is deliberately excluded: GPU Operator's
// operands reach /dev/nvidia* through the CDI spec, not through
// NVIDIA_DRIVER_ROOT, and its device nodes cannot be mirrored as regular
// files anyway.
var runMirroredDirs = []string{"usr", "proc", "config"}

// Apply mirrors the staged driver tree into /run/nvidia/driver, the path
// GPU Operator's own operands read by default (driverInstallDir in its chart
// values — mokka does not need to override NVIDIA_DRIVER_ROOT for it).
//
// This writes into that directory rather than replacing it (as a symlink
// swap would) because GPU Operator's validator can already have it
// bind-mounted into a container by the time Apply runs: NFD/GFD label the
// node from the pci-10de.present feature file pcibus writes during Stage,
// which is enough for the Operator to schedule the validator before this
// simulator's own Apply — gated behind every simulator's Stage — has run.
// A container's hostPath mount binds the directory's inode, not its path;
// replacing that path's directory entry afterwards (unlink + recreate,
// which is what a symlink swap does) orphans the mount permanently — the
// container keeps the pre-replacement, now-unlinked directory forever,
// regardless of what mokka does on the host next. Writing into the
// directory that's already there, instead of replacing it, keeps any
// earlier mount valid.
func (s *Simulator) Apply(_ context.Context, _ *agent.State) error {
	zap.L().Info("applying simulator", zap.String("simulator", name))
	s.ready.Store(false)

	driverRoot := s.host.RunPath("nvidia/driver")
	if err := ensureDriverRootDir(driverRoot); err != nil {
		return fmt.Errorf("ensure %s: %w", driverRoot, err)
	}

	for _, sub := range runMirroredDirs {
		src := s.host.RootPath("driver", sub)
		dst := s.host.RunPath("nvidia/driver", sub)
		if err := fsutil.MirrorTree(src, dst); err != nil {
			return fmt.Errorf("mirror %s into /run/nvidia/driver: %w", sub, err)
		}
	}

	s.ready.Store(true)
	return nil
}

// ensureDriverRootDir makes path a directory Apply can mirror into. An
// already-existing directory is left exactly as it is — replacing it is the
// mount-orphaning move Apply exists to avoid, regardless of who created it.
// Anything else there (a file, or a symlink to some other driver root) can't
// be mirrored into at all, so it is replaced, with a warning: unlike Revoke,
// Apply's job is to guarantee the path is ours going forward.
func ensureDriverRootDir(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0o755)
	}
	if err != nil {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	if fi.IsDir() {
		return nil
	}

	zap.L().Warn("driver root is not a directory; replacing it",
		zap.String("path", path), zap.String("type", fi.Mode().Type().String()))

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	return os.MkdirAll(path, 0o755)
}

// Revoke removes the content Apply mirrored into /run/nvidia/driver, leaving
// the directory itself in place — removing it would reintroduce the same
// mount-orphaning risk Apply avoids, for no benefit: nothing needs
// /run/nvidia/driver gone, only its content withdrawn. If the path is not a
// directory Apply could have mirrored into (a foreign file or symlink), it is
// left alone entirely: /run/nvidia is shared with the GPU Operator, and only
// content Apply could have written is ours to remove.
func (s *Simulator) Revoke(_ context.Context) error {
	zap.L().Info("revoking simulator", zap.String("simulator", name))
	s.ready.Store(false)

	driverRoot := s.host.RunPath("nvidia/driver")

	fi, err := os.Lstat(driverRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat %s: %w", driverRoot, err)
	}
	if !fi.IsDir() {
		zap.L().Warn("driver root is not our directory; leaving it",
			zap.String("path", driverRoot), zap.String("type", fi.Mode().Type().String()))

		return nil
	}

	var errs []error
	for _, sub := range runMirroredDirs {
		p := s.host.RunPath("nvidia/driver", sub)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}
