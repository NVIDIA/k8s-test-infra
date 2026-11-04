// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numatopology

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"go.uber.org/zap"
)

// ResolveState adds matching NUMA, PCI, and NVML placement to a state copy.
func ResolveState(ctx context.Context, h *host.Host, state *agent.State) (*agent.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := Resolve(h, state)
	if err != nil {
		return nil, err
	}
	resolved := *state
	if err := shareAffinities(&resolved, plan); err != nil {
		return nil, err
	}
	resolved.NodeShape.NUMA = plan
	zap.L().Debug("resolved node NUMA topology", zap.Int("zones", len(plan.Zones)))
	return &resolved, nil
}
