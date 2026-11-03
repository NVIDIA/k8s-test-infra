// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"go.uber.org/zap"
)

// Preparer resolves the layout shared by NUMA, PCI sysfs, and the NVML engine.
type Preparer struct{ host *host.Host }

// NewPreparer constructs a host-aware state preparer without changing the node.
func NewPreparer(h *host.Host) *Preparer { return &Preparer{host: h} }

// Prepare resolves CPU placement once and leaves the source observation intact.
func (p *Preparer) Prepare(ctx context.Context, state *agent.State) (*agent.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := resolve(p.host, state)
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

var _ agent.StatePreparer = (*Preparer)(nil)
