// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/nodecatalog"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/require"
)

func TestProjectionTargetDoesNotLiveGetNodeOutsideEligibleCache(t *testing.T) {
	live := &countingNodeGetter{node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node", UID: "node-uid",
	}}}
	snapshot := newInformerCache(nil, nil, nil, nodecatalog.New(), live, nil, DefaultOptions())
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
	cache := newInformerCache(nil, nil, nil, nodecatalog.New(), live, nil, Options{
		Workers: 1, LiveNodeGetTimeout: 2 * time.Hour,
	})
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Hour))
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

func TestKWOKReleaseWaitsForExactCleanup(t *testing.T) {
	t.Parallel()
	ref := &mokkav1alpha1.SGPUNodeReference{Name: "worker", UID: "worker-uid", AssignmentRevision: 7}
	rack := &mokkav1alpha1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid"},
		Spec:   mokkav1alpha1.SGPURackSpec{NodeType: mokkav1alpha1.NodeTypeKWOK, Nodes: []mokkav1alpha1.SGPURackNode{{Index: 0, NodeRef: ref}}},
		Status: mokkav1alpha1.SGPURackStatus{Delivery: &mokkav1alpha1.RackDelivery{Generation: 11}}}
	client := mokkafake.NewSimpleClientset(rack).MokkaV1alpha1()
	c := informerCache{liveRacks: client.SGPURacks(), liveAllocations: client.SGPURackAllocations(), liveTimeout: defaultLiveNodeGetTimeout}
	needed := release.ForSlot(rack, rack.Spec.Nodes[0], release.NodeGone)
	require.ErrorIs(t, c.PublishRelease(t.Context(), needed, 0), release.ErrCleanupPending)
	record := mokkav1alpha1.NewSGPURackAllocation(rack)
	record.MarkSlotReleased(0, ref.UID, 6)
	record, err := c.liveAllocations.Create(t.Context(), record, metav1.CreateOptions{})
	require.NoError(t, err)
	require.ErrorIs(t, c.PublishRelease(t.Context(), needed, 0), release.ErrCleanupPending)
	record.MarkSlotReleased(0, ref.UID, 8)
	_, err = c.liveAllocations.Update(t.Context(), record, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.NoError(t, c.PublishRelease(t.Context(), needed, 0))
	live, err := c.LiveRack(t.Context(), rack.Name)
	require.NoError(t, err)
	require.EqualValues(t, 12, live.Status.Delivery.Generation)
}
