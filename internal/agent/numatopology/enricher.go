// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numatopology

import (
	"context"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"go.uber.org/zap"
)

// Enricher resolves the NUMA layout from this node's real CPUs and memory and
// the zones the profile declares. It writes only State.NodeShape.NUMA, which
// the numa, pcibus, and gpudriver simulators all read.
type Enricher struct {
	host *host.Host
}

// NewEnricher returns an Enricher that reads the node through h.
func NewEnricher(h *host.Host) *Enricher { return &Enricher{host: h} }

// Name identifies this enricher in logs.
func (e *Enricher) Name() string { return "numa-topology" }

// Enrich sets state.NodeShape.NUMA, or leaves it nil on error.
func (e *Enricher) Enrich(ctx context.Context, state *agent.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	layout, err := Resolve(e.host, state)
	if err != nil {
		return err
	}
	state.NodeShape.NUMA = layout
	zap.L().Debug("resolved node NUMA topology", zap.Int("zones", len(layout.Zones)))
	return nil
}

var _ agent.Enricher = (*Enricher)(nil)
