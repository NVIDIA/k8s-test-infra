// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

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
		{name: "leader-election name", mutate: func(config *Config) {
			config.LeaderElection.Name = "Invalid_Name"
		}},
		{name: "leader-election namespace", mutate: func(config *Config) {
			config.LeaderElection.Namespace = "invalid.namespace"
		}},
		{name: "workers", mutate: func(config *Config) { config.Controller.Workers = 0 }},
		{name: "status interval", mutate: func(config *Config) {
			config.Controller.StatusProgressInterval = time.Millisecond
		}},
		{name: "lease", mutate: func(config *Config) {
			config.LeaderElection.LeaseDuration = config.LeaderElection.RenewDeadline
		}},
		{name: "negative lease durations", mutate: func(config *Config) {
			config.LeaderElection.RetryPeriod = -time.Second
		}},
		{name: "live Node GET timeout", mutate: func(config *Config) {
			config.Controller.LiveNodeGetTimeout = 0
		}},
		{name: "qps", mutate: func(config *Config) { config.Kubernetes.QPS = 0 }},
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
	election := newLeaderElectionConfig(config, lock, func(context.Context) {}, nil)

	require.True(t, election.ReleaseOnCancel)
	require.Equal(t, config.LeaderElection.LeaseDuration, election.LeaseDuration)
	require.Equal(t, config.LeaderElection.RenewDeadline, election.RenewDeadline)
	require.Equal(t, config.LeaderElection.RetryPeriod, election.RetryPeriod)

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

func TestElectionReadinessCoversStandbyAndLeaderLifecycle(t *testing.T) {
	readiness := newElectionReadiness()
	leaderReady := false
	isReady := func() bool { return readiness.ready(leaderReady) }

	require.False(t, isReady(), "a replica must not be ready before leader election starts")
	readiness.start()
	require.False(t, isReady(), "a replica must not be ready before it reaches the Lease")

	readiness.observeLeader(false)
	require.True(t, isReady(), "a standby that observes the elected leader can take over")

	readiness.observeLeader(true)
	require.False(t, isReady(), "a newly elected leader must wait for its informer caches")
	readiness.observeLeader(false)
	require.False(t, isReady(), "a delayed standby callback must not downgrade the elected state")

	leaderReady = true
	require.True(t, isReady(), "an elected leader is ready after its caches synchronize")

	readiness.stop()
	require.False(t, isReady(), "a stopped election participant must not remain ready")
	readiness.observeLeader(false)
	readiness.observeLeader(true)
	require.False(t, isReady(), "callbacks arriving after shutdown must be ignored")
}

func TestLeaderConfigPublishesElectionReadiness(t *testing.T) {
	config := DefaultConfig()
	lock := &fakeResourceLock{identity: "replica"}
	readiness := newElectionReadiness()
	readiness.start()
	election := newLeaderElectionConfig(config, lock, func(context.Context) {}, readiness)

	election.Callbacks.OnNewLeader("other-replica")
	require.True(t, readiness.ready(false))

	election.Callbacks.OnNewLeader(lock.Identity())
	require.False(t, readiness.ready(false))
	require.True(t, readiness.ready(true))

	election.Callbacks.OnStoppedLeading()
	require.False(t, readiness.ready(true))
}

func TestLeaderElectionDrainsWorkBeforeRelease(t *testing.T) {
	t.Run("SIGTERM", func(t *testing.T) { testLeaderElectionDrain(t, false) })
	t.Run("leader loss", func(t *testing.T) { testLeaderElectionDrain(t, true) })
}

func TestLeaderElectionStopsWorkBeforeReleaseGet(t *testing.T) {
	config := DefaultConfig()
	config.LeaderElection.LeaseDuration = 2 * time.Second
	config.LeaderElection.RenewDeadline = 500 * time.Millisecond
	config.LeaderElection.RetryPeriod = 10 * time.Millisecond
	lock := newElectionResourceLock(true)
	lock.delayReleaseGet = true
	lock.releaseGetStarted = make(chan struct{})
	lock.continueReleaseGet = make(chan struct{})
	readiness := newElectionReadiness()
	workStarted := make(chan struct{})
	workStopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		result <- runLeaderElection(ctx, config, lock, func(workCtx context.Context) error {
			close(workStarted)
			<-workCtx.Done()
			close(workStopped)
			return nil
		}, readiness)
	}()
	t.Cleanup(func() {
		cancel()
		lock.continueRelease()
		select {
		case <-runDone:
		case <-time.After(time.Second):
		}
	})

	requireClosed(t, workStarted, time.Second, "controller work did not start")
	require.True(t, readiness.ready(true))
	requireClosed(t, lock.releaseGetStarted, 2*time.Second, "leader election did not begin release GET")
	requireAlreadyClosed(t, workStopped, "controller work remained active when the release GET began")
	require.False(t, readiness.ready(true), "a replica that lost leadership must not remain ready")
	select {
	case <-result:
		t.Fatal("leader election returned before the release GET completed")
	default:
	}

	lock.continueRelease()
	select {
	case err := <-result:
		require.EqualError(t, err, "leader election ended unexpectedly")
	case <-time.After(time.Second):
		t.Fatal("leader election did not finish after the release GET completed")
	}
}

func requireClosed(t *testing.T, signal <-chan struct{}, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(timeout):
		t.Fatal(message)
	}
}

func requireAlreadyClosed(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	default:
		t.Fatal(message)
	}
}

//nolint:cyclop // The shared helper verifies both lease-loss and caller-cancellation drain paths.
func testLeaderElectionDrain(t *testing.T, loseLease bool) {
	t.Helper()
	config := DefaultConfig()
	config.LeaderElection.LeaseDuration = 200 * time.Millisecond
	config.LeaderElection.RenewDeadline = 60 * time.Millisecond
	config.LeaderElection.RetryPeriod = 10 * time.Millisecond
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
		}, nil)
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
		if loseLease {
			require.EqualError(t, err, "leader election ended unexpectedly")
		} else {
			require.NoError(t, err)
		}
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

func TestLeaderWorkUsesLeadingContext(t *testing.T) {
	work := newLeaderWork()
	outerCtx, cancelOuter := context.WithCancel(context.Background())
	t.Cleanup(cancelOuter)
	leadingCtx, stopLeading := context.WithCancel(outerCtx)
	started := make(chan struct{})
	callback := work.onStartedLeading(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}, func() {})
	go callback(leadingCtx)
	<-started

	stopLeading()
	select {
	case <-work.done:
	case <-time.After(time.Second):
		t.Fatal("controller work did not stop with the leadership context")
	}
	require.NoError(t, outerCtx.Err(), "the outer election context should remain active")
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
	mu                  sync.Mutex
	record              rl.LeaderElectionRecord
	acquired            bool
	failAfterAcquire    bool
	delayReleaseGet     bool
	releaseGetStarted   chan struct{}
	continueReleaseGet  chan struct{}
	continueReleaseOnce sync.Once
	releaseGetOnce      sync.Once
	released            chan struct{}
	releaseOnce         sync.Once
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

func (f *electionResourceLock) Get(ctx context.Context) (*rl.LeaderElectionRecord, []byte, error) {
	f.mu.Lock()
	acquired := f.acquired
	delayReleaseGet := f.delayReleaseGet
	record := f.record
	f.mu.Unlock()
	if acquired && delayReleaseGet {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		f.releaseGetOnce.Do(func() { close(f.releaseGetStarted) })
		select {
		case <-f.continueReleaseGet:
		case <-ctx.Done():
			return nil, nil, context.Cause(ctx)
		}
		f.mu.Lock()
		record = f.record
		f.mu.Unlock()
	}
	return &record, nil, nil
}

func (f *electionResourceLock) Create(context.Context, rl.LeaderElectionRecord) error {
	return errors.New("unexpected Lease creation")
}

func (f *electionResourceLock) Update(ctx context.Context, record rl.LeaderElectionRecord) error {
	f.mu.Lock()
	if record.HolderIdentity == "" {
		f.record = record
		f.releaseOnce.Do(func() { close(f.released) })
		f.mu.Unlock()
		return nil
	}
	if f.acquired && f.failAfterAcquire {
		delayReleaseGet := f.delayReleaseGet
		f.mu.Unlock()
		if delayReleaseGet {
			<-ctx.Done()
			return context.Cause(ctx)
		}
		return errors.New("lost Lease renewal")
	}
	f.acquired = true
	f.record = record
	f.mu.Unlock()
	return nil
}

func (f *electionResourceLock) continueRelease() {
	if f.continueReleaseGet != nil {
		f.continueReleaseOnce.Do(func() { close(f.continueReleaseGet) })
	}
}

func (f *electionResourceLock) RecordEvent(string) {}
func (f *electionResourceLock) Identity() string   { return "replica" }
func (f *electionResourceLock) Describe() string   { return "test/lease" }
