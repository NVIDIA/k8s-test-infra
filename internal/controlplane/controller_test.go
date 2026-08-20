// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rl "k8s.io/client-go/tools/leaderelection/resourcelock"
)

func TestValidateControllerConfig(t *testing.T) {
	require.NoError(t, ValidateControllerConfig(DefaultConfig()))

	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "workers", mutate: func(config *Config) { config.Workers = 0 }},
		{name: "status interval", mutate: func(config *Config) { config.StatusProgressInterval = time.Millisecond }},
		{name: "lease", mutate: func(config *Config) { config.LeaseDuration = config.RenewDeadline }},
		{name: "negative lease durations", mutate: func(config *Config) { config.RetryPeriod = -time.Second }},
		{name: "live Node GET timeout", mutate: func(config *Config) { config.LiveNodeGetTimeout = 0 }},
		{name: "qps", mutate: func(config *Config) { config.KubeAPIQPS = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			test.mutate(&config)
			require.Error(t, ValidateControllerConfig(config))
		})
	}
}

func TestLeaderConfigWaitsForControllerBeforeLeaseRelease(t *testing.T) {
	config := DefaultConfig()
	baseLock := &fakeResourceLock{identity: "replica"}
	workDone := make(chan struct{})
	workCanceled := make(chan struct{})
	lock := &drainingLock{
		Interface: baseLock, workDone: workDone,
		stopWork: func() { close(workCanceled) },
	}
	election := newLeaderElectionConfig(config, lock, func(context.Context) {})

	require.True(t, election.ReleaseOnCancel)
	require.Equal(t, config.LeaseDuration, election.LeaseDuration)
	require.Equal(t, config.RenewDeadline, election.RenewDeadline)
	require.Equal(t, config.RetryPeriod, election.RetryPeriod)

	released := make(chan struct{})
	go func() {
		_ = lock.Update(context.Background(), rl.LeaderElectionRecord{})
		close(released)
	}()
	<-workCanceled
	require.Zero(t, baseLock.updateCalls(), "the Lease was released before controller shutdown")
	close(workDone)
	<-released
	require.Equal(t, 1, baseLock.updateCalls())
}

func TestLeaderElectionDrainsWorkBeforeRelease(t *testing.T) {
	t.Run("SIGTERM", func(t *testing.T) { testLeaderElectionDrain(t, false) })
	t.Run("leader loss", func(t *testing.T) { testLeaderElectionDrain(t, true) })
}

func testLeaderElectionDrain(t *testing.T, loseLease bool) {
	t.Helper()
	config := DefaultConfig()
	config.LeaseDuration = 200 * time.Millisecond
	config.RenewDeadline = 60 * time.Millisecond
	config.RetryPeriod = 10 * time.Millisecond
	lock := newElectionResourceLock(loseLease)
	workStarted := make(chan struct{})
	workStopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runLeaderElection(ctx, config, lock, func(workCtx context.Context) error {
			close(workStarted)
			<-workCtx.Done()
			close(workStopped)
			return nil
		})
	}()

	select {
	case <-workStarted:
	case <-time.After(time.Second):
		t.Fatal("controller work did not start")
	}
	if !loseLease {
		cancel()
	}
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("leader election did not drain")
	}
	select {
	case <-workStopped:
	default:
		t.Fatal("leader election returned before controller work stopped")
	}
	select {
	case <-lock.released:
	default:
		t.Fatal("leader election did not release its Lease")
	}
}

func TestLeaderWorkDoesNotStartAfterElectionReturns(t *testing.T) {
	work := newLeaderWork()
	require.False(t, work.finishElection())
	called := false
	work.start(context.Background(), func(context.Context) error {
		called = true
		return nil
	}, func() {})

	require.False(t, called)
	select {
	case <-work.done:
		t.Fatal("late callback claimed work ownership")
	default:
	}
}

type fakeResourceLock struct {
	mu       sync.Mutex
	identity string
	updates  int
}

func (f *fakeResourceLock) Get(context.Context) (*rl.LeaderElectionRecord, []byte, error) {
	return &rl.LeaderElectionRecord{}, nil, nil
}

func (f *fakeResourceLock) Create(context.Context, rl.LeaderElectionRecord) error { return nil }

func (f *fakeResourceLock) Update(context.Context, rl.LeaderElectionRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates++
	return nil
}

func (f *fakeResourceLock) RecordEvent(string) {}
func (f *fakeResourceLock) Identity() string   { return f.identity }
func (f *fakeResourceLock) Describe() string   { return "test/lease" }

func (f *fakeResourceLock) updateCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updates
}

type electionResourceLock struct {
	mu               sync.Mutex
	record           rl.LeaderElectionRecord
	acquired         bool
	failAfterAcquire bool
	released         chan struct{}
	releaseOnce      sync.Once
}

func newElectionResourceLock(failAfterAcquire bool) *electionResourceLock {
	now := time.Now()
	return &electionResourceLock{
		record: rl.LeaderElectionRecord{
			HolderIdentity: "replica", LeaseDurationSeconds: 1,
			AcquireTime: metav1.NewTime(now), RenewTime: metav1.NewTime(now),
		},
		failAfterAcquire: failAfterAcquire,
		released:         make(chan struct{}),
	}
}

func (f *electionResourceLock) Get(context.Context) (*rl.LeaderElectionRecord, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.record
	return &record, nil, nil
}

func (f *electionResourceLock) Create(context.Context, rl.LeaderElectionRecord) error {
	return errors.New("unexpected Lease creation")
}

func (f *electionResourceLock) Update(_ context.Context, record rl.LeaderElectionRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record.HolderIdentity == "" {
		f.record = record
		f.releaseOnce.Do(func() { close(f.released) })
		return nil
	}
	if f.acquired && f.failAfterAcquire {
		return errors.New("lost Lease renewal")
	}
	f.acquired = true
	f.record = record
	return nil
}

func (f *electionResourceLock) RecordEvent(string) {}
func (f *electionResourceLock) Identity() string   { return "replica" }
func (f *electionResourceLock) Describe() string   { return "test/lease" }
