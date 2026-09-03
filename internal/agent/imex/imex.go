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
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const name = "imex"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator fakes the IMEX kernel surface that is absent on CPU-only nodes
//
// Main consumers
// - the DRA compute-domain kubelet plugin (discover device majors at startup)
// - containerd (must find the channel chardevs on disk before it admits a pod
// carrying a compute-domain CDI spec).
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

// Stage materializes the IMEX capability surface under host.Root.
// It is a no-op (but marks ready) when state.IMEX.Enabled is false.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Debug("staging simulator", zap.String("simulator", name))

	if !state.IMEX.Enabled {
		s.ready.Store(true)
		zap.L().Debug("simulator staged; imex disabled", zap.String("simulator", name))
		return nil
	}

	if err := stageChannelDevs(s.host, state); err != nil {
		return fmt.Errorf("imex channel devs: %w", err)
	}
	if err := stageProcDevices(s.host, state, s.procDevicesPath); err != nil {
		return fmt.Errorf("imex proc-devices: %w", err)
	}
	if err := stageFabricImexMgmt(s.host); err != nil {
		return fmt.Errorf("imex fabric-imex-mgmt: %w", err)
	}

	s.ready.Store(true)
	zap.L().Debug("simulator staged", zap.String("simulator", name))
	return nil
}

// Discard removes all IMEX surfaces staged by Stage.
// It is a no-op when Stage never completed successfully.
func (s *Simulator) Discard(_ context.Context) error {
	if !s.ready.Load() {
		return nil
	}
	zap.L().Debug("discarding simulator", zap.String("simulator", name))

	var errs []error
	for _, rel := range []string{
		"driver/dev/nvidia-caps-imex-channels",
		"imex",
	} {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	capFile := s.host.RootPath("driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt")
	if err := fsutil.Remove(capFile); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
