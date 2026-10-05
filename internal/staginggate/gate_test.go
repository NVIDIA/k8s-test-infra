// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package staginggate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStagingLockExcludesAdjustments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), FileName)
	exclusive, err := Exclusive(context.Background(), path)
	require.NoError(t, err)
	shared, acquired, err := Shared(path)
	require.NoError(t, err)
	require.False(t, acquired, "NRI must not inspect a tree being staged")
	require.Nil(t, shared)
	require.NoError(t, exclusive.Close())

	shared, acquired, err = Shared(path)
	require.NoError(t, err)
	require.True(t, acquired)
	started := make(chan struct{})
	type result struct {
		lock *Lock
		err  error
	}
	locked := make(chan result, 1)
	go func() {
		close(started)
		lock, lockErr := Exclusive(context.Background(), path)
		locked <- result{lock: lock, err: lockErr}
	}()
	<-started
	require.Never(t, func() bool { return len(locked) != 0 }, 30*time.Millisecond, time.Millisecond,
		"agent teardown must wait for in-flight adjustments")
	require.NoError(t, shared.Close())
	select {
	case next := <-locked:
		require.NoError(t, next.err)
		require.NoError(t, next.lock.Close())
	case <-time.After(time.Second):
		t.Fatal("agent did not acquire the lock after NRI released it")
	}
}

func TestExclusiveGivesUpWhenContextEnds(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), FileName)
	holder, err := Exclusive(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, holder.Close())
	shared, acquired, err := Shared(path)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { require.NoError(t, shared.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	lock, err := Exclusive(ctx, path)
	require.ErrorIs(t, err, context.DeadlineExceeded, "a held shared lock must not hang the agent")
	require.Nil(t, lock)
}
