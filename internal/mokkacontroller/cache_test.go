// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	controllernodes "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/nodecatalog"
	"github.com/stretchr/testify/require"
)

func TestLiveNodeFallbackUsesCallerContextAndDeadline(t *testing.T) {
	live := newBlockingNodeGetter(false)
	cache := newInformerCache(nil, nil, nil, controllernodes.New(), live, Options{
		Workers: 1, LiveNodeGetTimeout: 2 * time.Hour,
	})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	result := make(chan error, 1)
	go func() {
		_, err := cache.Node(ctx, "node")
		result <- err
	}()

	requestCtx := <-live.started
	wantDeadline, exists := ctx.Deadline()
	require.True(t, exists)
	gotDeadline, exists := requestCtx.Deadline()
	require.True(t, exists)
	require.Equal(t, wantDeadline, gotDeadline)
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	<-live.finished
}

func TestLiveNodeFallbackTimeoutBoundsContextViolatingClient(t *testing.T) {
	const timeout = 20 * time.Millisecond
	live := newBlockingNodeGetter(true)
	t.Cleanup(func() {
		close(live.release)
		<-live.finished
	})
	cache := newInformerCache(nil, nil, nil, controllernodes.New(), live, Options{
		Workers: 1, LiveNodeGetTimeout: timeout,
	})
	result := make(chan error, 1)
	go func() {
		_, err := cache.Node(context.Background(), "node")
		result <- err
	}()

	requestCtx := <-live.started
	deadline, exists := requestCtx.Deadline()
	require.True(t, exists)
	require.LessOrEqual(t, time.Until(deadline), timeout)
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("live Node fallback exceeded its request timeout")
	}
}

type blockingNodeGetter struct {
	corev1client.NodeInterface
	started            chan context.Context
	finished           chan struct{}
	release            chan struct{}
	ignoreCancellation bool
}

func newBlockingNodeGetter(ignoreCancellation bool) *blockingNodeGetter {
	return &blockingNodeGetter{
		started: make(chan context.Context, 1), finished: make(chan struct{}),
		release: make(chan struct{}), ignoreCancellation: ignoreCancellation,
	}
}

func (g *blockingNodeGetter) Get(ctx context.Context, _ string, _ metav1.GetOptions) (*corev1.Node, error) {
	defer close(g.finished)
	g.started <- ctx
	if g.ignoreCancellation {
		<-g.release
		return nil, errors.New("released context-violating Node GET")
	}
	<-ctx.Done()
	return nil, context.Cause(ctx)
}
