// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	controllernodes "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/nodecatalog"
	"github.com/stretchr/testify/require"
)

func TestProjectionTargetDoesNotLiveGetNodeOutsideEligibleCache(t *testing.T) {
	live := &countingNodeGetter{node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node", UID: "node-uid",
	}}}
	snapshot := newInformerCache(nil, nil, nil, controllernodes.New(), live, DefaultOptions())
	rack := &mokkav1alpha1.SGPURack{Spec: mokkav1alpha1.SGPURackSpec{
		Nodes: []mokkav1alpha1.SGPURackNode{{
			Index: 0, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: live.node.Name, UID: live.node.UID},
		}},
	}}

	node, allowed, err := snapshot.ProjectionTarget(rack, &rack.Spec.Nodes[0])

	require.NoError(t, err)
	require.False(t, allowed)
	require.Nil(t, node)
	require.Zero(t, live.calls.Load())
}

func TestLiveNodeFallbackUsesCallerContextAndDeadline(t *testing.T) {
	live := newBlockingNodeGetter()
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

type blockingNodeGetter struct {
	corev1client.NodeInterface
	started  chan context.Context
	finished chan struct{}
}

type countingNodeGetter struct {
	corev1client.NodeInterface
	node  *corev1.Node
	calls atomic.Int64
}

func (g *countingNodeGetter) Get(
	_ context.Context,
	_ string,
	_ metav1.GetOptions,
) (*corev1.Node, error) {
	g.calls.Add(1)
	return g.node.DeepCopy(), nil
}

func newBlockingNodeGetter() *blockingNodeGetter {
	return &blockingNodeGetter{
		started: make(chan context.Context, 1), finished: make(chan struct{}),
	}
}

func (g *blockingNodeGetter) Get(ctx context.Context, _ string, _ metav1.GetOptions) (*corev1.Node, error) {
	defer close(g.finished)
	g.started <- ctx
	<-ctx.Done()
	return nil, context.Cause(ctx)
}
