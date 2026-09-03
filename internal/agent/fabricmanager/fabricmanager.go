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

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fabricmanager"
)

const name = "fabricmanager"

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Daemon    = (*Simulator)(nil)
)

// Simulator stages the directory shared with workloads. Daemon owns the
// marker lifecycle, including transitions between staged directories.
type Simulator struct {
	staged  atomic.Bool
	enabled atomic.Bool
	daemon  *fabricmanager.Daemon
}

// Options configures the simulator.
type Options struct {
	// InitDelay withholds readiness after the daemon starts, reproducing the
	// window during which real GPUs report IN_PROGRESS while registering.
	InitDelay time.Duration
}

// New returns a fabricmanager Simulator.
func New(opts Options) *Simulator {
	return &Simulator{daemon: fabricmanager.NewDaemon(fabricmanager.Config{InitDelay: opts.InitDelay})}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether Stage succeeded and the daemon has published any
// required marker.
func (s *Simulator) Ready() bool { return s.staged.Load() && s.daemon.Ready() }

// Stage creates the marker directory and gives it to the daemon. An empty
// directory means fabricmanager is disabled on this node.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.staged.Store(false)
	zap.L().Debug("staging simulator", zap.String("simulator", name))

	dir := ""
	if state.Fabric.ManagerStateDir != "" {
		dir = h.HostPath(state.Fabric.ManagerStateDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	if enabled := dir != ""; enabled != s.enabled.Swap(enabled) {
		if enabled {
			zap.L().Info("fabricmanager simulation enabled", zap.String("state_dir", dir))
		} else {
			zap.L().Info("fabricmanager simulation disabled")
		}
	}

	s.daemon.Reload(dir)
	s.staged.Store(true)
	zap.L().Debug("simulator staged", zap.String("simulator", name))
	return nil
}

// Run keeps fabric readiness published until the agent shuts down.
func (s *Simulator) Run(ctx context.Context) error { return s.daemon.Serve(ctx) }

// Reload is a no-op: Stage delivers every revision to the daemon.
func (s *Simulator) Reload(_ context.Context, _ *agent.State) error { return nil }

// Discard withdraws readiness so GPUs do not report COMPLETED after shutdown.
func (s *Simulator) Discard(_ context.Context, _ *host.Host) error {
	s.staged.Store(false)
	zap.L().Debug("discarding simulator", zap.String("simulator", name))
	return s.daemon.Stop()
}
