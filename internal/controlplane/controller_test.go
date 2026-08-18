// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	lock := &drainingLock{Interface: baseLock, workDone: workDone}
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
	select {
	case <-released:
		t.Fatal("the Lease was released before controller shutdown")
	case <-time.After(20 * time.Millisecond):
	}
	close(workDone)
	require.Eventually(t, func() bool {
		select {
		case <-released:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.Equal(t, 1, baseLock.updateCalls())
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
