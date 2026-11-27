// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/staginggate"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type preparingSimulator struct {
	mockSimApplier
	prepare func(context.Context, *State) error
}

func (s *preparingSimulator) PrepareStage(ctx context.Context, state *State) error {
	return s.prepare(ctx, state)
}

type stageObserver struct {
	mockSimApplier
	observe func() error
}

func (s *stageObserver) Stage(ctx context.Context, state *State) error {
	if err := s.observe(); err != nil {
		return err
	}
	return s.mockSimApplier.Stage(ctx, state)
}

func TestPreparationFinishesUnderLockBeforeAnyStage(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "stage.lock")
	var prepared atomic.Bool
	observer := &stageObserver{mockSimApplier: mockSimApplier{name: "observer"}, observe: func() error {
		if !prepared.Load() {
			return errors.New("staging ran before host preparation")
		}
		return nil
	}}
	preparer := &preparingSimulator{mockSimApplier: mockSimApplier{name: "preparer"}, prepare: func(context.Context, *State) error {
		lock, acquired, err := staginggate.Shared(lockPath)
		require.NoError(t, err)
		if acquired {
			require.NoError(t, lock.Close())
		}
		require.False(t, acquired, "preparation must hold the staging lock")
		prepared.Store(true)
		return nil
	}}
	a := New(Config{Simulators: []Simulator{observer, preparer}, Log: zap.NewNop(), StagingLockPath: lockPath})
	require.NoError(t, a.reconcile(t.Context(), &State{}))
	require.EqualValues(t, 1, observer.stageCalls.Load())
	require.EqualValues(t, 1, preparer.applyCalls.Load())
}

func TestPreparationFailurePreventsEveryStageAndCanRetry(t *testing.T) {
	t.Parallel()
	failure := errors.New("restore failed")
	observer := &mockSimApplier{name: "observer"}
	preparer := &preparingSimulator{mockSimApplier: mockSimApplier{name: "preparer"}, prepare: func(context.Context, *State) error { return failure }}
	a := New(Config{Simulators: []Simulator{observer, preparer}, Log: zap.NewNop()})
	require.ErrorIs(t, a.reconcile(t.Context(), &State{}), failure)
	require.Zero(t, observer.stageCalls.Load())
	require.Zero(t, preparer.stageCalls.Load())
	require.Zero(t, observer.applyCalls.Load())
	require.False(t, a.Liveness().OK)
	require.False(t, a.Staged().OK)
	failure = nil
	require.NoError(t, a.reconcile(t.Context(), &State{}))
	require.EqualValues(t, 1, observer.stageCalls.Load())
	require.EqualValues(t, 1, preparer.applyCalls.Load())
	require.True(t, a.Liveness().OK)
}
