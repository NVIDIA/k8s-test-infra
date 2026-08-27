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
	ready           atomic.Bool
	procDevicesPath string // /proc/devices in production; overridden in tests
}

// New returns an imex Simulator.
func New() *Simulator {
	return &Simulator{procDevicesPath: "/proc/devices"}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the IMEX capability surface under h.Root.
// It is a no-op (but marks ready) when state.IMEX.Enabled is false.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	if !state.IMEX.Enabled {
		s.ready.Store(true)
		return nil
	}

	if err := stageChannelDevs(h, state); err != nil {
		return fmt.Errorf("imex channel devs: %w", err)
	}
	if err := stageProcDevices(h, state, s.procDevicesPath); err != nil {
		return fmt.Errorf("imex proc-devices: %w", err)
	}
	if err := stageFabricImexMgmt(h); err != nil {
		return fmt.Errorf("imex fabric-imex-mgmt: %w", err)
	}

	s.ready.Store(true)
	return nil
}

// Discard removes all IMEX surfaces staged by Stage.
// It is a no-op when Stage never completed successfully.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	if !s.ready.Load() {
		return nil
	}

	var errs []error
	for _, rel := range []string{
		"driver/dev/nvidia-caps-imex-channels",
		"imex",
	} {
		p := filepath.Join(h.Root, rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	capFile := filepath.Join(h.Root, "driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt")
	if err := h.Remove(capFile); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
