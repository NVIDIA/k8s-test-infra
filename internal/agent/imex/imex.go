// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package imex implements the IMEX capability surface simulator:
// channel character devices, a /proc/devices overlay, and the
// fabric-imex-mgmt capability file consumed by the NVIDIA DRA
// compute-domain kubelet plugin.
package imex

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
	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

const name = "imex"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator fakes the IMEX kernel surface that is absent on CPU-only nodes.
//
// Main consumers
// - the DRA compute-domain kubelet plugin, which reads the device majors from
// the /proc/devices substitute at startup; its CDI specs carry the device
// numbers, so it never reads the channel nodes
// - the NRI plugin, which injects the staged channel0 into containers that opt
// in with the imex-channels annotation.
type Simulator struct {
	host            *host.Host
	ready           atomic.Bool
	procDevicesPath string // /proc/devices in production; overridden in tests
}

// New returns an imex Simulator.
func New(h *host.Host) *Simulator {
	return &Simulator{host: h, procDevicesPath: "/proc/devices"}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the IMEX capability surface under host.Root. When
// state.IMEX.Enabled is false it removes the surface, as Discard does, and
// marks ready: the flag follows the profile and can change at runtime.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	if !state.IMEX.Enabled {
		if err := s.remove(); err != nil {
			return fmt.Errorf("remove imex: %w", err)
		}
		s.ready.Store(true)
		zap.L().Info("simulator staged; imex disabled", zap.String("simulator", name))
		return nil
	}

	if err := stageKernelSurface(s.host, state, s.procDevicesPath); err != nil {
		// Fail open: the node becomes one without IMEX, which stays ready and
		// runs no ComputeDomains, rather than an agent that never gets ready.
		zap.L().Warn("cannot simulate the IMEX kernel surface; ComputeDomains fail on this node",
			zap.String("simulator", name), zap.Error(err))
		if err := s.remove(); err != nil {
			return fmt.Errorf("remove imex: %w", err)
		}
		s.ready.Store(true)
		return nil
	}

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

// Discard removes every IMEX surface Stage may have written. It does not
// depend on the last Stage having completed: a pass that failed partway can
// leave what an earlier pass staged, and removing what is absent is a no-op.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))
	return s.remove()
}

// remove deletes the IMEX capability surface.
func (s *Simulator) remove() error {
	var errs []error
	for _, rel := range []string{
		imex.ChannelsDir,
		filepath.Dir(imex.ProcDevicesPath),
	} {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	capFile := s.host.RootPath(imex.FabricIMEXMgmtPath)
	if err := fsutil.Remove(capFile); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
