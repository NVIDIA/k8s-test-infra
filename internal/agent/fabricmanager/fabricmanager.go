// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package fabricmanager simulates nv-fabricmanager, the service that registers
// GPUs with the NVSwitches on an NVSwitch platform before they are usable.
package fabricmanager

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fabricmanager"
)

const (
	name = "fabricmanager"

	// stateDirRel is where the marker lands in the agent's mount namespace. The
	// chart points fabricmanager.stateDir and the host-fabric-state hostPath at
	// the same directory, so the workload-side reader and this writer see one
	// file through two mounts.
	stateDirRel = "fabric-state"

	// reassertInterval matches what the mock NVML engine tolerates between
	// checks. The marker sits on a hostPath that outlives the pod, so it is
	// rewritten rather than written once: see Run.
	reassertInterval = 2 * time.Second
)

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Daemon    = (*Simulator)(nil)
)

// Simulator publishes fabric readiness for GPUs whose profile sets
// `fabric.state: auto`. The mock NVML engine reads the marker to resolve each
// GPU between IN_PROGRESS and COMPLETED, which is how workloads end up waiting
// on the fabric the way they do on real NVSwitch hardware.
type Simulator struct {
	ready   atomic.Bool
	serving atomic.Bool

	// enabled mirrors whether the last Stage saw a fabricmanager deployment.
	// Run consults it because the agent has no Host to re-derive it from.
	enabled atomic.Bool
	// initDelay simulates the registration latency of the real daemon.
	initDelay time.Duration
	// stateDir is captured by Stage; Run is called without a Host.
	stateDir atomic.Pointer[string]
}

// Options configures the simulator.
type Options struct {
	// InitDelay withholds the marker for this long after Run starts, so tests
	// can observe the IN_PROGRESS state real hardware passes through.
	InitDelay time.Duration
}

// New returns a fabricmanager Simulator.
func New(opts Options) *Simulator {
	return &Simulator{initDelay: opts.InitDelay}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether Stage succeeded and, where a marker is expected, it has
// been written.
func (s *Simulator) Ready() bool {
	if !s.ready.Load() {
		return false
	}
	return !s.enabled.Load() || s.serving.Load()
}

// Stage creates the marker directory. A profile that declares NVLink does not
// imply fabricmanager runs, so the deployment decides: the chart sets the state
// dir only where it also mounts it, making a non-empty value the one signal
// that both exist on this node.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	if state.Fabric.ManagerStateDir == "" {
		s.enabled.Store(false)
		s.ready.Store(true)
		return nil
	}

	dir := h.RootPath(stateDirRel)
	if err := h.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.stateDir.Store(&dir)
	s.enabled.Store(true)

	s.ready.Store(true)
	return nil
}

// Run publishes readiness and holds it until the agent shuts down.
func (s *Simulator) Run(ctx context.Context) error {
	dir := s.stateDir.Load()
	if !s.enabled.Load() || dir == nil {
		return nil
	}

	// The hostPath outlives the pod, so a marker left by a previous one would
	// report COMPLETED before this fabricmanager had registered anything.
	if err := fabricmanager.RemoveReady(*dir); err != nil {
		slog.Warn("could not clear stale readiness marker", "simulator", name, "err", err)
	}
	if !s.awaitRegistration(ctx) {
		return nil
	}

	// Re-assert rather than write once: the marker is a plain file on a shared
	// hostPath, and anything that removes it must not silently strand every GPU
	// on the node at IN_PROGRESS.
	t := time.NewTicker(reassertInterval)
	defer t.Stop()
	for {
		s.assert(*dir)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// awaitRegistration simulates the latency of registering with the NVSwitches.
// Reports false when the agent shut down first.
func (s *Simulator) awaitRegistration(ctx context.Context) bool {
	if s.initDelay <= 0 {
		return true
	}
	slog.Info("simulating fabric registration delay", "simulator", name, "delay", s.initDelay)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(s.initDelay):
		return true
	}
}

// assert writes the marker, logging only the transitions so a 2s loop does not
// fill the log with confirmations that nothing changed.
func (s *Simulator) assert(dir string) {
	if err := fabricmanager.WriteReady(dir); err != nil {
		s.serving.Store(false)
		slog.Error("could not write readiness marker", "simulator", name, "err", err)
		return
	}
	if s.serving.CompareAndSwap(false, true) {
		slog.Info("fabric ready", "simulator", name, "marker", fabricmanager.MarkerPath(dir))
	}
}

// Reload is a no-op: the marker carries no state derived from the profile, so a
// changed State cannot alter what Run publishes.
func (s *Simulator) Reload(_ context.Context, _ *agent.State) error { return nil }

// Discard removes the marker so a GPU does not report COMPLETED against a
// fabricmanager that is no longer running.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	if !s.ready.Load() {
		return nil
	}
	s.serving.Store(false)
	return fabricmanager.RemoveReady(h.RootPath(stateDirRel))
}
