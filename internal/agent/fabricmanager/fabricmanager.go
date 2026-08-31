// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package fabricmanager simulates nv-fabricmanager, the service that registers
// GPUs with the NVSwitches on an NVSwitch platform before they are usable.
package fabricmanager

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fabricmanager"
)

const name = "fabricmanager"

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Daemon    = (*Simulator)(nil)
)

// Simulator publishes fabric readiness for GPUs whose profile sets
// `fabric.state: auto`. The mock NVML engine reads the marker to resolve each
// GPU between IN_PROGRESS and COMPLETED, which is how workloads end up waiting
// on the fabric the way they do on real NVSwitch hardware.
type Simulator struct {
	initDelay time.Duration

	ready atomic.Bool
	// daemon is built by Stage, the only method holding a Host. Nil means this
	// node has no fabricmanager.
	daemon atomic.Pointer[fabricmanager.Daemon]
	// stateDir is the staged marker directory, kept so Discard can withdraw a
	// marker left behind by a reconcile that has since disabled the daemon.
	stateDir atomic.Pointer[string]
}

// Options configures the simulator.
type Options struct {
	// InitDelay withholds readiness after the daemon starts, reproducing the
	// window during which real GPUs report IN_PROGRESS while registering.
	InitDelay time.Duration
}

// New returns a fabricmanager Simulator.
func New(opts Options) *Simulator { return &Simulator{initDelay: opts.InitDelay} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether Stage succeeded and, where a marker is expected, the
// daemon has published it.
func (s *Simulator) Ready() bool {
	if !s.ready.Load() {
		return false
	}

	d := s.daemon.Load()
	return d == nil || d.Ready()
}

// Stage creates the marker directory. A profile that declares NVLink does not
// imply fabricmanager runs, so the deployment decides: the chart sets the state
// dir only where it also mounts it, making a non-empty value the one signal
// that both exist on this node.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	if state.Fabric.ManagerStateDir == "" {
		s.daemon.Store(nil)
		s.ready.Store(true)
		return nil
	}

	dir := h.HostPath(state.Fabric.ManagerStateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Built once: Run holds a reference for the agent's lifetime, so replacing
	// it on a later reconcile would leave the running daemon orphaned.
	if s.daemon.Load() == nil {
		s.daemon.Store(fabricmanager.NewDaemon(fabricmanager.Config{
			StateDir:  dir,
			InitDelay: s.initDelay,
		}))
	}

	s.stateDir.Store(&dir)

	s.ready.Store(true)
	return nil
}

// Run keeps readiness published until the agent shuts down.
func (s *Simulator) Run(ctx context.Context) error {
	d := s.daemon.Load()

	if d == nil {
		return nil
	}

	return d.Serve(ctx)
}

// Reload is a no-op: the marker carries no state derived from the profile, so a
// changed State cannot alter what the daemon publishes.
func (s *Simulator) Reload(_ context.Context, _ *agent.State) error { return nil }

// Discard withdraws readiness so a GPU does not report COMPLETED against a
// fabricmanager that is no longer running.
func (s *Simulator) Discard(_ context.Context, _ *host.Host) error {
	if !s.ready.Load() {
		return nil
	}

	if d := s.daemon.Load(); d != nil {
		return d.Stop()
	}

	if dir := s.stateDir.Load(); dir != nil {
		return fabricmanager.RemoveReady(*dir)
	}

	return nil
}
