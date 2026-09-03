// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// Simulator owns one simulated component's reconciliation lifecycle.
// Stage and Discard are exact inverses.
type Simulator interface {
	Name() string
	// Stage materializes artifacts that are not yet externally visible.
	// Re-runs on every State change and must be idempotent.
	Stage(ctx context.Context, h *host.Host, state *State) error
	// Discard is the inverse of Stage and runs only on shutdown.
	Discard(ctx context.Context, h *host.Host) error
	// Ready reports whether the simulator is successfully done staging and publishing its artifacts.
	Ready() bool
}

// Applier is implemented by simulators whose staged artifacts something outside
// the node acts on (containerd, NFD, the GPU Operator validator).
// Only gpudriver, pcibus, and cdi implement this interface.
type Applier interface {
	// Apply publishes artifacts after all Stage calls have completed (barrier).
	Apply(ctx context.Context, h *host.Host, state *State) error
	// Revoke is the inverse of Apply and runs on shutdown before Discard.
	Revoke(ctx context.Context, h *host.Host) error
}

// Daemon is implemented by simulators that supervise a long-running background
// process (fabricmanager marker loop, mock-ib server). The agent launches Run
// on the Stage barrier of the first successful reconcile, so a daemon may
// assume its own Stage artifacts exist.
type Daemon interface {
	// Run blocks until ctx is cancelled. ctx is the agent's lifetime, so a Run
	// survives every subsequent state update.
	Run(ctx context.Context) error
	// Reload delivers later States to the running daemon, so a profile edit
	// applies without a pod restart. No-op for daemons holding no staged state.
	Reload(ctx context.Context, state *State) error
}
