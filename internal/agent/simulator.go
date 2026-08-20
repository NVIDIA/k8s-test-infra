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
	// Ready reports whether the last Stage call succeeded.
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
// once at startup; it must block until ctx is cancelled.
type Daemon interface {
	Run(ctx context.Context) error
}
