// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package main

import (
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	rl "k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/stretchr/testify/require"
)

func TestOptionsDefaultsAndFlags(t *testing.T) {
	options := defaultOptions()
	require.Equal(t, "mokka-controller.mokka.nvidia.com", options.LeaseName)
	require.Equal(t, 15*time.Second, options.LeaseDuration)
	require.Equal(t, 10*time.Second, options.RenewDeadline)
	require.Equal(t, 2*time.Second, options.RetryPeriod)
	require.Equal(t, ":8081", options.HealthBindAddress)
	require.Equal(t, 100*time.Millisecond, options.StatusDebounce)
	require.Equal(t, time.Second, options.StatusProgressInterval)

	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	options.addFlags(flags)
	require.NoError(t, flags.Parse([]string{
		"--kubeconfig=/tmp/config", "--leader-election-namespace=mokka", "--workers=7",
		"--status-debounce=250ms", "--status-progress-interval=2s",
	}))
	require.Equal(t, "/tmp/config", options.Kubeconfig)
	require.Equal(t, "mokka", options.LeaseNamespace)
	require.Equal(t, 7, options.Workers)
	require.Equal(t, 250*time.Millisecond, options.StatusDebounce)
	require.Equal(t, 2*time.Second, options.StatusProgressInterval)
	require.NoError(t, options.validate())
}

func TestProbeHandlerReadiness(t *testing.T) {
	ready := false
	handler := newProbeHandler(func() bool { return ready })

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, response.Code)

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	ready = true
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, response.Code)
}

func TestLeaderConfigAndReleaseWaitForControllerShutdown(t *testing.T) {
	options := defaultOptions()
	baseLock := &fakeResourceLock{identity: "replica"}
	workDone := make(chan struct{})
	lock := &drainingLock{Interface: baseLock, workDone: workDone}
	config := newLeaderElectionConfig(options, lock, func(context.Context) {})

	require.True(t, config.ReleaseOnCancel)
	require.Equal(t, options.LeaseDuration, config.LeaseDuration)
	require.Equal(t, options.RenewDeadline, config.RenewDeadline)
	require.Equal(t, options.RetryPeriod, config.RetryPeriod)

	released := make(chan struct{})
	go func() {
		_ = lock.Update(context.Background(), rl.LeaderElectionRecord{})
		close(released)
	}()
	select {
	case <-released:
		t.Fatal("the lease was released before controller shutdown")
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

func TestLeaderCallbackRunsControllerForLeadershipContext(t *testing.T) {
	options := defaultOptions()
	lock := &fakeResourceLock{identity: "replica"}
	started := make(chan struct{})
	finished := make(chan struct{})
	config := newLeaderElectionConfig(options, lock, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	})

	ctx, cancel := context.WithCancel(context.Background())
	go config.Callbacks.OnStartedLeading(ctx)
	<-started
	cancel()
	<-finished
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
