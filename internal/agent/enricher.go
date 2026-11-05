// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// After a failed enrichment, the retry delay starts at enrichRetryFirst and
// doubles up to enrichRetryLimit.
const (
	enrichRetryFirst = 5 * time.Second
	enrichRetryLimit = 5 * time.Minute
)

// Enricher adds facts read from this node to a State before any simulator sees
// it. Sources describe only what the user wants, so a file and a control plane
// can produce the same State; whatever depends on the node itself belongs here.
type Enricher interface {
	Name() string
	// Enrich reads the node and writes its result into the State fields it
	// owns. It must not change the node.
	Enrich(ctx context.Context, state *State) error
}

// enrich runs every enricher, in order, on a copy of state, so a later
// enricher can use an earlier one's result. If one fails, enrich drops the
// copy: simulators share enriched fields, so they get all of them or none.
func (a *Agent) enrich(ctx context.Context, state *State) (*State, error) {
	if len(a.enrichers) == 0 {
		return state, nil
	}
	enriched := *state
	for _, e := range a.enrichers {
		if err := e.Enrich(ctx, &enriched); err != nil {
			return nil, fmt.Errorf("enricher %s: %w", e.Name(), err)
		}
	}
	return &enriched, nil
}

// pendingState is a source State that is waiting to be enriched.
type pendingState struct {
	state *State
	delay time.Duration    // wait before the next retry
	retry <-chan time.Time // nil while no retry is scheduled
}

// enrichOrRetry enriches p.state, reconciles the result and returns it. On
// failure it schedules a retry and returns latest unchanged, like a source
// error: the node keeps the last fully enriched State, and a requeue
// restages that State, never one with missing fields.
func (a *Agent) enrichOrRetry(ctx context.Context, p *pendingState, latest *State) *State {
	enriched, err := a.enrich(ctx, p.state)
	if err != nil {
		if p.delay == 0 {
			p.delay = a.enrichRetryFirst
		}
		a.log.Error("enrichment failed; keeping the last enriched state",
			zap.Int64("generation", p.state.Generation), zap.Duration("retry_in", p.delay), zap.Error(err))
		p.retry = time.After(p.delay)
		p.delay = min(2*p.delay, enrichRetryLimit)
		return latest
	}
	*p = pendingState{}
	a.reconcileLogged(ctx, enriched)
	return enriched
}
