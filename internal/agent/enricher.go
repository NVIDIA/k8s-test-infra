// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"

	"go.uber.org/zap"
)

// Enricher adds facts read from this node to a State before any simulator sees
// it. Sources describe only what the user wants, so a file and a control plane
// can produce the same State; whatever depends on the node itself belongs here.
type Enricher interface {
	Name() string
	// Enrich reads the node and writes its result into the State fields it
	// owns. It must not change the node, and on error it must write nothing.
	Enrich(ctx context.Context, state *State) error
}

// enrich runs every enricher, in order, on a copy of state, so a later
// enricher can use an earlier one's result. A failed enricher leaves its
// fields empty and the rest still run: only the simulators that need its
// result stop, by reporting not ready.
func (a *Agent) enrich(ctx context.Context, state *State) *State {
	if len(a.enrichers) == 0 {
		return state
	}
	enriched := *state
	for _, e := range a.enrichers {
		if err := e.Enrich(ctx, &enriched); err != nil {
			a.log.Error("enricher failed; simulators that need its result stay not ready",
				zap.String("enricher", e.Name()), zap.Error(err))
		}
	}
	return &enriched
}
