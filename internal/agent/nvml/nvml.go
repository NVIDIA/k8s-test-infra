// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvml implements the simulator for what Mokka serves in place of the
// driver's userspace: the GPU character devices, the mock NVML and CUDA
// libraries, nvidia-smi, the engine config with the board's MIG table, and the
// machine type GFD reads. The overlay serves them to containers, and the
// gpudriver simulator publishes them as the driver root.
package nvml

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const name = "nvml"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator implements agent.Simulator.
type Simulator struct {
	host  *host.Host
	ready atomic.Bool
}

// New returns an nvml Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the userspace under host.Root/driver/ and the engine
// config under host.Root/config/. All surfaces run in parallel; a failure in
// any one cancels the rest via gctx.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return stageCharDevs(gctx, s.host, state) })
	g.Go(func() error { return stageNVMLShim(gctx, s.host, state) })
	g.Go(func() error { return stageCUDAShim(gctx, s.host, state) })
	g.Go(func() error { return stageNvidiaSMI(gctx, s.host, state) })
	g.Go(func() error { return writeEngineConfig(gctx, s.host, state) })
	g.Go(func() error { return writeMachineType(gctx, s.host, state) })

	if err := g.Wait(); err != nil {
		return err
	}

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

// stagedPaths lists the paths that Discard removes, in removal order (leaves
// first). RemoveAll on the whole driver/ tree is intentionally avoided: the
// gpudriver, ib and pcibus simulators stage files there too, and those must
// survive Discard.
var stagedPaths = []string{
	"driver/dev",
	"driver/usr/lib64",
	"driver/usr/bin/nvidia-smi",
	"driver/usr/bin/nvidia-smi.sh",
	"driver/config/config.yaml",
	machineTypeRel,
	"config/config.yaml",
}

// Discard removes the paths Stage writes. Every path is owned by nvml, so
// removing absent or partially staged paths is safe.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	var errs []error

	for _, rel := range stagedPaths {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}
