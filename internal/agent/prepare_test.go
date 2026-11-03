// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type preparerFunc func(context.Context, *State) (*State, error)

func (f preparerFunc) Prepare(ctx context.Context, state *State) (*State, error) {
	return f(ctx, state)
}

type preparedStateObserver struct {
	mockSimApplier
	staged, applied *State
}

func (s *preparedStateObserver) Stage(_ context.Context, state *State) error {
	s.staged = state
	s.stageCalls.Add(1)
	return nil
}
func (s *preparedStateObserver) Apply(_ context.Context, state *State) error {
	s.applied = state
	s.applyCalls.Add(1)
	return nil
}

func TestPrepareSharesOneStateBeforeParallelStaging(t *testing.T) {
	t.Parallel()
	input := &State{NodeShape: NodeShape{NumGPUs: 8}}
	first := &preparedStateObserver{mockSimApplier: mockSimApplier{name: "first"}}
	second := &preparedStateObserver{mockSimApplier: mockSimApplier{name: "second"}}
	calls := 0
	var prepared *State
	a := New(Config{
		Simulators: []Simulator{first, second}, Log: zap.NewNop(),
		Preparer: preparerFunc(func(_ context.Context, state *State) (*State, error) {
			require.Same(t, input, state)
			// No simulator may start the next wave before preparation finishes.
			require.Equal(t, int32(calls), first.stageCalls.Load())
			require.Equal(t, int32(calls), second.stageCalls.Load())
			calls++
			copyState := *state
			copyState.NodeShape.NumGPUs = 16
			prepared = &copyState
			return prepared, nil
		}),
	})
	for range 2 {
		require.NoError(t, a.reconcile(t.Context(), input))
		for _, observer := range []*preparedStateObserver{first, second} {
			require.Same(t, prepared, observer.staged)
			require.Same(t, prepared, observer.applied)
			require.Equal(t, 16, observer.staged.NodeShape.NumGPUs)
		}
	}
	require.Equal(t, 2, calls)
	require.Equal(t, 8, input.NodeShape.NumGPUs, "requeue input must remain unchanged")
}

func TestPreparationFailureClosesStagingGateAndCanRecover(t *testing.T) {
	t.Parallel()
	for _, nilState := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "nil state"}[nilState], func(t *testing.T) {
			t.Parallel()
			sim := &mockSimApplier{name: "test"}
			fail := false
			a := New(Config{Simulators: []Simulator{sim}, Log: zap.NewNop(), Preparer: preparerFunc(func(_ context.Context, state *State) (*State, error) {
				if !fail {
					return state, nil
				}
				if nilState {
					return nil, nil
				}
				return state, errors.New("invalid host topology")
			})})
			require.NoError(t, a.reconcile(t.Context(), &State{}))
			fail = true
			require.ErrorContains(t, a.reconcile(t.Context(), &State{}), "prepare state")
			require.Equal(t, int32(1), sim.stageCalls.Load())
			require.Equal(t, int32(1), sim.applyCalls.Load())
			require.False(t, a.Staged().OK)
			require.False(t, a.Liveness().OK)
			fail = false
			require.NoError(t, a.reconcile(t.Context(), &State{}))
			require.True(t, a.Staged().OK)
			require.True(t, a.Liveness().OK)
			require.Equal(t, int32(2), sim.applyCalls.Load())
		})
	}
}

type preparedDaemonObserver struct {
	preparedStateObserver
	reloaded *State
}

func (s *preparedDaemonObserver) Run(context.Context) error { return nil }
func (s *preparedDaemonObserver) Reload(_ context.Context, state *State) error {
	s.reloaded = state
	return nil
}

func TestDaemonReloadReceivesPreparedState(t *testing.T) {
	t.Parallel()
	input := &State{}
	prepared := &State{NodeShape: NodeShape{NumGPUs: 16}}
	sim := &preparedDaemonObserver{preparedStateObserver: preparedStateObserver{mockSimApplier: mockSimApplier{name: "daemon"}}}
	a := New(Config{Simulators: []Simulator{sim}, Source: newChanSource(Update{State: input}, Update{State: input}), Log: zap.NewNop(), Preparer: preparerFunc(func(context.Context, *State) (*State, error) { return prepared, nil })})
	require.NoError(t, a.Run(t.Context()))
	require.Same(t, prepared, sim.staged)
	require.Same(t, prepared, sim.reloaded)
	require.Same(t, prepared, sim.applied)
}
