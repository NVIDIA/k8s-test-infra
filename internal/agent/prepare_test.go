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

type preparingSimulator struct {
	*preparedStateObserver
	prepare func(context.Context, *State) (*State, error)
}

func (s *preparingSimulator) Prepare(ctx context.Context, state *State) (*State, error) {
	return s.prepare(ctx, state)
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

func TestPrepareChainsSimulatorsBeforeParallelStaging(t *testing.T) {
	t.Parallel()
	input := &State{NodeShape: NodeShape{NumGPUs: 8}}
	first := &preparingSimulator{preparedStateObserver: &preparedStateObserver{mockSimApplier: mockSimApplier{name: "first"}}}
	plain := &preparedStateObserver{mockSimApplier: mockSimApplier{name: "plain"}}
	second := &preparingSimulator{preparedStateObserver: &preparedStateObserver{mockSimApplier: mockSimApplier{name: "second"}}}
	observers := []*preparedStateObserver{first.preparedStateObserver, plain, second.preparedStateObserver}
	var intermediate, prepared *State
	var order []string
	rounds := 0
	assertNotStaged := func() {
		t.Helper()
		for _, observer := range observers {
			require.Equal(t, int32(rounds), observer.stageCalls.Load())
		}
	}
	first.prepare = func(_ context.Context, state *State) (*State, error) {
		assertNotStaged()
		require.Same(t, input, state)
		order = append(order, "first")
		copyState := *state
		copyState.NodeShape.NumGPUs += 4
		intermediate = &copyState
		return intermediate, nil
	}
	second.prepare = func(_ context.Context, state *State) (*State, error) {
		assertNotStaged()
		require.Same(t, intermediate, state)
		order = append(order, "second")
		copyState := *state
		copyState.NodeShape.NumGPUs *= 2
		prepared = &copyState
		return prepared, nil
	}
	a := New(Config{Simulators: []Simulator{first, plain, second}, Log: zap.NewNop()})
	for range 2 {
		require.NoError(t, a.reconcile(t.Context(), input))
		for _, observer := range observers {
			require.Same(t, prepared, observer.staged)
			require.Same(t, prepared, observer.applied)
			require.Equal(t, 24, observer.staged.NodeShape.NumGPUs)
		}
		rounds++
	}
	require.Equal(t, []string{"first", "second", "first", "second"}, order)
	require.Equal(t, 12, intermediate.NodeShape.NumGPUs, "later preparation must preserve earlier input")
	require.Equal(t, 8, input.NodeShape.NumGPUs, "requeue input must remain unchanged")
}

func TestPreparationFailureClosesStagingGateAndCanRecover(t *testing.T) {
	t.Parallel()
	for _, nilState := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "nil state"}[nilState], func(t *testing.T) {
			t.Parallel()
			sim := &preparingSimulator{preparedStateObserver: &preparedStateObserver{mockSimApplier: mockSimApplier{name: "test"}}}
			fail := false
			sim.prepare = func(_ context.Context, state *State) (*State, error) {
				if !fail {
					return state, nil
				}
				if nilState {
					return nil, nil
				}
				return state, errors.New("invalid host topology")
			}
			laterCalls := 0
			later := &preparingSimulator{preparedStateObserver: &preparedStateObserver{mockSimApplier: mockSimApplier{name: "later"}}, prepare: func(_ context.Context, state *State) (*State, error) { laterCalls++; return state, nil }}
			a := New(Config{Simulators: []Simulator{sim, later}, Log: zap.NewNop()})
			require.NoError(t, a.reconcile(t.Context(), &State{}))
			fail = true
			require.ErrorContains(t, a.reconcile(t.Context(), &State{}), "prepare test")
			require.Equal(t, 1, laterCalls, "a failed preparer must stop the preparation chain")
			for _, observer := range []*preparedStateObserver{sim.preparedStateObserver, later.preparedStateObserver} {
				require.Equal(t, int32(1), observer.stageCalls.Load())
				require.Equal(t, int32(1), observer.applyCalls.Load())
			}
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
	preparingSimulator
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
	sim := &preparedDaemonObserver{preparingSimulator: preparingSimulator{preparedStateObserver: &preparedStateObserver{mockSimApplier: mockSimApplier{name: "daemon"}}, prepare: func(context.Context, *State) (*State, error) { return prepared, nil }}}
	a := New(Config{Simulators: []Simulator{sim}, Source: newChanSource(Update{State: input}, Update{State: input}), Log: zap.NewNop()})
	require.NoError(t, a.Run(t.Context()))
	require.Same(t, prepared, sim.staged)
	require.Same(t, prepared, sim.reloaded)
	require.Same(t, prepared, sim.applied)
}
