// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type sourceStateObserver struct {
	mockSimApplier
	staged, applied *State
}

func (s *sourceStateObserver) Stage(_ context.Context, state *State) error {
	s.staged = state
	s.stageCalls.Add(1)
	return nil
}
func (s *sourceStateObserver) Apply(_ context.Context, state *State) error {
	s.applied = state
	s.applyCalls.Add(1)
	return nil
}

type sourceDaemonObserver struct {
	sourceStateObserver
	reloaded *State
}

func (s *sourceDaemonObserver) Run(context.Context) error { return nil }
func (s *sourceDaemonObserver) Reload(_ context.Context, state *State) error {
	s.reloaded = state
	return nil
}

func TestSourceStateReachesStagingApplyAndDaemonReload(t *testing.T) {
	t.Parallel()
	state := &State{NodeShape: NodeShape{NUMA: &NUMATopology{Zones: []NUMAZone{{ID: 0, CPUs: []int{0, 1}, MemoryKB: 8000}}}}}
	sim := &sourceDaemonObserver{sourceStateObserver: sourceStateObserver{mockSimApplier: mockSimApplier{name: "daemon"}}}
	a := New(Config{Simulators: []Simulator{sim}, Source: newChanSource(Update{State: state}, Update{State: state}), Log: zap.NewNop()})
	require.NoError(t, a.Run(t.Context()))
	require.Same(t, state, sim.staged)
	require.Same(t, state, sim.reloaded)
	require.Same(t, state, sim.applied)
}
