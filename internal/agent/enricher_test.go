// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type enricherFunc struct {
	name string
	fn   func(*State) error
}

func (e enricherFunc) Name() string                             { return e.name }
func (e enricherFunc) Enrich(_ context.Context, s *State) error { return e.fn(s) }

// Enrichers run in order on a copy of the source's State. A failed enricher
// does not stop the ones after it, or the reconcile.
func TestEnrichersRunInOrderOnACopyBeforeStage(t *testing.T) {
	t.Parallel()

	source := &State{Generation: 3}
	layout := &NUMATopology{Zones: []NUMAZone{{ID: 0, CPUs: []int{0}}}}
	var order []string
	var seenByLast *NUMATopology
	enrichers := []Enricher{
		enricherFunc{"first", func(s *State) error {
			order = append(order, "first")
			s.NodeShape.NUMA = layout
			return nil
		}},
		enricherFunc{"failing", func(*State) error {
			order = append(order, "failing")
			return errors.New("node unreadable")
		}},
		enricherFunc{"last", func(s *State) error {
			order = append(order, "last")
			seenByLast = s.NodeShape.NUMA
			return nil
		}},
	}
	sim := &sourceStateObserver{mockSimApplier: mockSimApplier{name: "sim"}}
	a := New(Config{Simulators: []Simulator{sim}, Enrichers: enrichers, Source: newChanSource(Update{State: source}), Log: zap.NewNop()})
	require.NoError(t, a.Run(t.Context()))

	require.Equal(t, []string{"first", "failing", "last"}, order)
	require.Same(t, layout, seenByLast, "a later enricher sees an earlier one's result")
	require.Nil(t, source.NodeShape.NUMA, "the source's State is not changed")
	require.Same(t, layout, sim.staged.NodeShape.NUMA)
	require.Same(t, sim.staged, sim.applied)
}

// A requeue restages the enriched State without reading the node again.
func TestRequeueDoesNotEnrichAgain(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	count := enricherFunc{"count", func(*State) error { calls.Add(1); return nil }}
	sim := &requeueSim{
		readySim: readySim{name: "requeue", ready: true},
		requeue:  make(chan struct{}),
		staged:   make(chan *State, 4),
	}
	updates := make(chan Update, 1)
	a := New(Config{Simulators: []Simulator{sim}, Enrichers: []Enricher{count}, Source: &chanSource{ch: updates}, Log: zap.NewNop()})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	updates <- Update{State: &State{Generation: 7}}
	enriched := <-sim.staged
	sim.requeue <- struct{}{}
	require.Same(t, enriched, <-sim.staged)
	require.EqualValues(t, 1, calls.Load())

	cancel()
	require.NoError(t, <-done)
}
