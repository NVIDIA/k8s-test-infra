// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"go.uber.org/zap"
)

// Prepare resolves CPU placement once and leaves the source observation intact.
func (s *Simulator) Prepare(ctx context.Context, state *agent.State) (*agent.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.opts.Enabled {
		return state, nil
	}
	plan, err := resolve(s.host, state)
	if err != nil {
		return nil, err
	}
	prepared := *state
	if err := shareAffinities(&prepared, plan); err != nil {
		return nil, err
	}
	prepared.NodeShape.NUMA = plan
	zap.L().Debug("prepared node NUMA topology", zap.Int("zones", len(plan.Zones)))
	return &prepared, nil
}

var _ agent.StatePreparer = (*Simulator)(nil)
