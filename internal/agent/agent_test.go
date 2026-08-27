// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// mockSimApplier implements Simulator and Applier.
// stageFailN controls how many Stage calls fail before succeeding.
type mockSimApplier struct {
	name       string
	stageFailN atomic.Int32 // decremented per call; negative = always succeed
	stageCalls atomic.Int32
	applyCalls atomic.Int32
}

func (m *mockSimApplier) Name() string { return m.name }
func (m *mockSimApplier) Stage(_ context.Context, _ *host.Host, _ *State) error {
	m.stageCalls.Add(1)
	if m.stageFailN.Add(-1) >= 0 {
		return errors.New("stage error")
	}
	return nil
}
func (m *mockSimApplier) Discard(_ context.Context, _ *host.Host) error { return nil }
func (m *mockSimApplier) Ready() bool                                   { return m.stageFailN.Load() < 0 }
func (m *mockSimApplier) Apply(_ context.Context, _ *host.Host, _ *State) error {
	m.applyCalls.Add(1)
	return nil
}
func (m *mockSimApplier) Revoke(_ context.Context, _ *host.Host) error { return nil }

// chanSource is a finite StateSource backed by a pre-filled, closed channel.
type chanSource struct{ ch chan Update }

func newChanSource(updates ...Update) *chanSource {
	ch := make(chan Update, len(updates))
	for _, u := range updates {
		ch <- u
	}
	close(ch)
	return &chanSource{ch: ch}
}

func (s *chanSource) Watch(_ context.Context) <-chan Update { return s.ch }
func (s *chanSource) Close() error                          { return nil }

func newAgent(t *testing.T, sim *mockSimApplier, updates ...Update) *Agent {
	t.Helper()
	return New(Config{
		Simulators: []Simulator{sim},
		Source:     newChanSource(updates...),
		Host:       host.New(t.TempDir()),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func runAgent(t *testing.T, a *Agent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.Run(ctx)
}

// TestReconcile_StageFailurePreventsApply pins the ordering invariant:
// Apply must never be called when any Stage returns an error.
func TestReconcile_StageFailurePreventsApply(t *testing.T) {
	sim := &mockSimApplier{name: "test"}
	sim.stageFailN.Store(1) // fail the first (only) call
	a := newAgent(t, sim, Update{State: &State{}, At: time.Now()})
	runAgent(t, a)

	require.Equal(t, int32(1), sim.stageCalls.Load())
	require.Equal(t, int32(0), sim.applyCalls.Load(), "Apply must not run when Stage fails")
	require.False(t, a.Live())
}

// TestReconcile_StageSuccessRunsApply verifies Apply is called after Stage succeeds.
func TestReconcile_StageSuccessRunsApply(t *testing.T) {
	sim := &mockSimApplier{name: "test"} // stageFailN == 0 → first Add(-1) = -1 < 0 → success
	a := newAgent(t, sim, Update{State: &State{}, At: time.Now()})
	runAgent(t, a)

	require.Equal(t, int32(1), sim.stageCalls.Load())
	require.Equal(t, int32(1), sim.applyCalls.Load())
	require.True(t, a.Live())
}

// TestAgentLiveness_Recovery verifies Live() becomes true again once Stage succeeds.
func TestAgentLiveness_Recovery(t *testing.T) {
	sim := &mockSimApplier{name: "test"}
	sim.stageFailN.Store(1) // first Stage fails, second succeeds
	a := newAgent(t, sim,
		Update{State: &State{Generation: 1}, At: time.Now()},
		Update{State: &State{Generation: 2}, At: time.Now()},
	)
	runAgent(t, a)

	require.Equal(t, int32(2), sim.stageCalls.Load())
	require.True(t, a.Live(), "Live must recover once Stage succeeds")
}
