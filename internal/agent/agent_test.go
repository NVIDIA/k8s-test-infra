// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/staginggate"
)

// mockSimApplier implements Simulator and Applier.
// stageFailN controls how many Stage calls fail before succeeding.
type mockSimApplier struct {
	name       string
	stageFailN atomic.Int32 // decremented per call; negative = always succeed
	stageCalls atomic.Int32
	applyCalls atomic.Int32
	discards   atomic.Int32
	applyErr   error
	// discardExpired records whether Discard ran on an already-expired context,
	// which is how a teardown with no budget left shows up.
	discardExpired atomic.Bool
	// duringTeardown, when set, runs from Revoke and Discard so a test can
	// inspect the agent while it tears down.
	duringTeardown func()
}

func (m *mockSimApplier) Name() string { return m.name }
func (m *mockSimApplier) Stage(_ context.Context, _ *State) error {
	m.stageCalls.Add(1)
	if m.stageFailN.Add(-1) >= 0 {
		return errors.New("stage error")
	}
	return nil
}
func (m *mockSimApplier) Discard(ctx context.Context) error {
	m.discards.Add(1)
	m.discardExpired.Store(ctx.Err() != nil)
	if m.duringTeardown != nil {
		m.duringTeardown()
	}
	return nil
}
func (m *mockSimApplier) Ready() bool { return m.stageFailN.Load() < 0 }
func (m *mockSimApplier) Apply(_ context.Context, _ *State) error {
	m.applyCalls.Add(1)
	return m.applyErr
}
func (m *mockSimApplier) Revoke(_ context.Context) error {
	if m.duringTeardown != nil {
		m.duringTeardown()
	}
	return nil
}

// mockDaemon implements Simulator, Applier and Daemon, logging each synchronous
// lifecycle call so tests can pin the wave ordering. Run is counted rather than
// logged: it runs on its own goroutine, so its position is not deterministic.
type mockDaemon struct {
	name      string
	reloadErr error

	mu    sync.Mutex
	calls []string

	runCalls    atomic.Int32
	reloadCalls atomic.Int32
}

func (m *mockDaemon) record(call string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
}

func (m *mockDaemon) callLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

func (m *mockDaemon) Name() string { return m.name }
func (m *mockDaemon) Stage(_ context.Context, _ *State) error {
	m.record("stage")
	return nil
}
func (m *mockDaemon) Discard(_ context.Context) error { return nil }
func (m *mockDaemon) Ready() bool                     { return true }
func (m *mockDaemon) Apply(_ context.Context, _ *State) error {
	m.record("apply")
	return nil
}
func (m *mockDaemon) Revoke(_ context.Context) error { return nil }

// Run returns immediately instead of blocking on ctx, so errgroup.Wait finishes
// when the source drains rather than idling out the test timeout.
func (m *mockDaemon) Run(_ context.Context) error {
	m.runCalls.Add(1)
	return nil
}

func (m *mockDaemon) Reload(_ context.Context, _ *State) error {
	m.reloadCalls.Add(1)
	m.record("reload")
	return m.reloadErr
}

// readySim is a Simulator whose readiness is set directly, so a probe test can
// pin one simulator down without also driving a reconcile.
type readySim struct {
	name  string
	ready bool
}

func (s readySim) Name() string                            { return s.name }
func (s readySim) Stage(_ context.Context, _ *State) error { return nil }
func (s readySim) Discard(_ context.Context) error         { return nil }
func (s readySim) Ready() bool                             { return s.ready }

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
	return newAgentWith(t, sim, updates...)
}

func newAgentWith(t *testing.T, sim Simulator, updates ...Update) *Agent {
	t.Helper()
	return New(Config{
		Simulators: []Simulator{sim},
		Source:     newChanSource(updates...),
		Log:        zap.NewNop(),
	})
}

func runAgent(t *testing.T, a *Agent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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

	live := a.Liveness()
	require.False(t, live.OK)
	require.Equal(t, "stage failed: test", live.Reason, "/healthz must name the simulator that failed")
	require.False(t, a.Staged().OK, "/stagedz must remain closed when initial staging fails")
}

// TestReconcile_StageSuccessRunsApply verifies Apply is called after Stage succeeds.
func TestReconcile_StageSuccessRunsApply(t *testing.T) {
	sim := &mockSimApplier{name: "test"} // stageFailN == 0 → first Add(-1) = -1 < 0 → success
	a := newAgent(t, sim, Update{State: &State{}, At: time.Now()})
	runAgent(t, a)

	require.Equal(t, int32(1), sim.stageCalls.Load())
	require.Equal(t, int32(1), sim.applyCalls.Load())
	require.True(t, a.Liveness().OK)
	require.False(t, a.Staged().OK, "a stopped agent must not advertise staged files")
}

func TestStagedIgnoresSimulatorServiceReadiness(t *testing.T) {
	t.Parallel()

	updates := make(chan Update, 1)
	updates <- Update{State: &State{}, At: time.Now()}
	a := New(Config{
		Simulators: []Simulator{readySim{name: "delayed", ready: false}},
		Source:     &chanSource{ch: updates},
		Log:        zap.NewNop(),
	})
	require.False(t, a.Staged().OK)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	require.Eventually(t, func() bool { return a.Staged().OK }, time.Second, time.Millisecond,
		"a staged tree must open the gate while a simulator is still not ready")
	require.False(t, a.Readiness().OK, "ongoing simulator readiness remains independent")

	cancel()
	require.NoError(t, <-done)
	require.False(t, a.Staged().OK, "a stopped agent must close the staged gate")
}

func TestAgentClosesStagedGateBeforeTeardown(t *testing.T) {
	t.Parallel()

	lockPath := t.TempDir() + "/" + staginggate.FileName
	sim := &mockSimApplier{name: "test"}
	updates := make(chan Update, 1)
	updates <- Update{State: &State{}, At: time.Now()}
	a := New(Config{
		Simulators:      []Simulator{sim},
		Source:          &chanSource{ch: updates},
		Log:             zap.NewNop(),
		StagingLockPath: lockPath,
	})
	// Teardown may proceed without the lock, which is safe only because
	// /stagedz already reports unstaged by then.
	var stagedDuringTeardown atomic.Bool
	sim.duringTeardown = func() {
		if a.Staged().OK {
			stagedDuringTeardown.Store(true)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	require.Eventually(t, func() bool { return a.Staged().OK }, time.Second, time.Millisecond)
	lock, acquired, err := staginggate.Shared(lockPath)
	require.NoError(t, err)
	require.True(t, acquired, "a staged agent must release its exclusive staging lock")
	require.NoError(t, lock.Close())

	cancel()
	require.NoError(t, <-done)
	require.Equal(t, int32(1), sim.discards.Load())
	require.False(t, stagedDuringTeardown.Load(), "/stagedz must report unstaged before teardown starts")
	require.False(t, a.Staged().OK, "a stopped agent must not advertise staged files")
}

func TestTeardownProceedsWhenStagingLockIsHeld(t *testing.T) {
	t.Parallel()

	lockPath := t.TempDir() + "/" + staginggate.FileName
	sim := &mockSimApplier{name: "test"}
	updates := make(chan Update, 1)
	updates <- Update{State: &State{}, At: time.Now()}
	a := New(Config{
		Simulators:      []Simulator{sim},
		Source:          &chanSource{ch: updates},
		Log:             zap.NewNop(),
		ShutdownTimeout: 200 * time.Millisecond,
		StagingLockPath: lockPath,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	require.Eventually(t, func() bool { return a.Staged().OK }, time.Second, time.Millisecond)

	holder, acquired, err := staginggate.Shared(lockPath)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { require.NoError(t, holder.Close()) })

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a lock that outlives the grace period must not fail shutdown")
	case <-time.After(2 * time.Second):
		t.Fatal("a held staging lock hung agent shutdown")
	}
	require.Equal(t, int32(1), sim.discards.Load(), "teardown must still run without the lock")
	require.False(t, sim.discardExpired.Load(), "waiting for the lock must leave teardown part of its budget")
}

// TestApplyFailureKeepsStagedGateOpen pins /stagedz to Stage alone. Nothing
// retries a failed Apply until the state changes again, so an Apply failure
// that closed the gate would stop injection on the node until then.
func TestApplyFailureKeepsStagedGateOpen(t *testing.T) {
	t.Parallel()

	sim := &mockSimApplier{name: "test", applyErr: errors.New("apply error")}
	updates := make(chan Update, 2)
	a := New(Config{
		Simulators: []Simulator{sim},
		Source:     &chanSource{ch: updates},
		Log:        zap.NewNop(),
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	updates <- Update{State: &State{Generation: 1}, At: time.Now()}
	require.Eventually(t, func() bool { return sim.applyCalls.Load() == 1 }, time.Second, time.Millisecond)
	require.True(t, a.Staged().OK, "an initial Apply failure must not hold NRI back")

	updates <- Update{State: &State{Generation: 2}, At: time.Now()}
	require.Eventually(t, func() bool { return sim.applyCalls.Load() == 2 }, time.Second, time.Millisecond)
	require.True(t, a.Staged().OK, "a restage whose Apply fails must reopen the gate")

	cancel()
	require.NoError(t, <-done)
}

// TestSupervise_ReloadRunsBetweenStageAndApply pins the supervisor wave to the
// barrier: a daemon sees a state change after its Stage lands, before Apply.
func TestSupervise_ReloadRunsBetweenStageAndApply(t *testing.T) {
	sim := &mockDaemon{name: "daemon"}
	a := newAgentWith(t, sim,
		Update{State: &State{Generation: 1}, At: time.Now()},
		Update{State: &State{Generation: 2}, At: time.Now()},
	)
	runAgent(t, a)

	require.Equal(t,
		[]string{"stage", "apply", "stage", "reload", "apply"},
		sim.callLog(),
	)
}

// TestSupervise_RunStartsOnceAcrossReconciles verifies a daemon launches once
// however many states arrive; later ones reach it through Reload.
func TestSupervise_RunStartsOnceAcrossReconciles(t *testing.T) {
	sim := &mockDaemon{name: "daemon"}
	a := newAgentWith(t, sim,
		Update{State: &State{Generation: 1}, At: time.Now()},
		Update{State: &State{Generation: 2}, At: time.Now()},
		Update{State: &State{Generation: 3}, At: time.Now()},
	)
	runAgent(t, a)

	require.Equal(t, int32(1), sim.runCalls.Load(), "Run must be launched exactly once")
	require.Equal(t, int32(2), sim.reloadCalls.Load(), "Reload must run on every reconcile after the first")
}

// TestSupervise_ReloadErrorDoesNotFailReconcile verifies a failed Reload is
// logged and skipped, leaving the daemon on its previous state and Apply intact.
func TestSupervise_ReloadErrorDoesNotFailReconcile(t *testing.T) {
	sim := &mockDaemon{name: "daemon", reloadErr: errors.New("reload error")}
	a := newAgentWith(t, sim,
		Update{State: &State{Generation: 1}, At: time.Now()},
		Update{State: &State{Generation: 2}, At: time.Now()},
	)
	runAgent(t, a)

	require.Equal(t,
		[]string{"stage", "apply", "stage", "reload", "apply"},
		sim.callLog(),
	)
	require.True(t, a.Liveness().OK)
}

// TestSupervise_StageFailureSkipsSupervisorWave verifies a daemon does not start
// against surfaces Stage failed to write.
func TestSupervise_StageFailureSkipsSupervisorWave(t *testing.T) {
	sim := &failingStageDaemon{}
	a := newAgentWith(t, sim, Update{State: &State{}, At: time.Now()})
	runAgent(t, a)

	require.Equal(t, int32(0), sim.runCalls.Load(), "Run must not start when Stage fails")
	require.Equal(t, int32(0), sim.reloadCalls.Load())
}

// failingStageDaemon is a Daemon whose Stage always fails.
type failingStageDaemon struct{ mockDaemon }

func (f *failingStageDaemon) Stage(_ context.Context, _ *State) error {
	return errors.New("stage error")
}

// TestAgentLiveness_Recovery verifies liveness passes again once Stage succeeds.
func TestAgentLiveness_Recovery(t *testing.T) {
	sim := &mockSimApplier{name: "test"}
	sim.stageFailN.Store(1) // first Stage fails, second succeeds
	a := newAgent(t, sim,
		Update{State: &State{Generation: 1}, At: time.Now()},
		Update{State: &State{Generation: 2}, At: time.Now()},
	)
	runAgent(t, a)

	require.Equal(t, int32(2), sim.stageCalls.Load())
	require.True(t, a.Liveness().OK, "liveness must recover once Stage succeeds")
}

// One simulator that is not ready must sink the whole probe: health.Handler
// keys the status code off Probe.OK alone, so an aggregate that stayed true
// would serve 200 for a node that is not serving.
func TestAgentReadiness_AggregatesSimulators(t *testing.T) {
	for _, c := range []struct {
		name   string
		sims   []Simulator
		wantOK bool
		want   map[string]bool
	}{
		{
			name:   "all ready",
			sims:   []Simulator{readySim{"gpudriver", true}, readySim{"ib", true}},
			wantOK: true,
			want:   map[string]bool{"gpudriver": true, "ib": true},
		},
		{
			name:   "one not ready",
			sims:   []Simulator{readySim{"gpudriver", true}, readySim{"ib", false}},
			wantOK: false,
			want:   map[string]bool{"gpudriver": true, "ib": false},
		},
		{
			name:   "none ready",
			sims:   []Simulator{readySim{"gpudriver", false}, readySim{"ib", false}},
			wantOK: false,
			want:   map[string]bool{"gpudriver": false, "ib": false},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			probe := New(Config{Simulators: c.sims}).Readiness()

			require.Equal(t, c.wantOK, probe.OK)
			require.Len(t, probe.Components, len(c.want))
			for name, ready := range c.want {
				require.Equal(t, ready, probe.Components[name].OK,
					"/readyz must attribute the result to %s", name)
			}
		})
	}
}
