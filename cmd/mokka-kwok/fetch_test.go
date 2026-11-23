// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

type batchCall struct {
	racks []state.RackRequest
	reply chan batchReply
}

type batchReply struct {
	code       int
	retryAfter string
	results    []state.RackResult
}

func testFetcher(t *testing.T, opts batchOptions) (*rackFetcher, <-chan batchCall) {
	t.Helper()
	calls := make(chan batchCall, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request state.BatchRequest
		if r.Method != http.MethodPost || r.URL.Path != "/v1alpha1/racks/state:batchGet" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		call := batchCall{racks: request.Racks, reply: make(chan batchReply, 1)}
		select {
		case calls <- call:
		case <-r.Context().Done():
			return
		}
		select {
		case reply := <-call.reply:
			if reply.code != 0 {
				w.Header().Set("Retry-After", reply.retryAfter)
				w.WriteHeader(reply.code)
				return
			}
			if err := json.NewEncoder(w).Encode(state.BatchResponse{APIVersion: state.Version, Racks: reply.results}); err != nil {
				t.Log(err)
			}
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	pods := newShardPods()
	t.Cleanup(pods.backfills.ShutDown)
	f := newRackFetcher(config{controlPlane: server.URL, batch: opts}, newRackViews(pods, func(string) {}))
	f.retries = workqueue.NewTypedItemExponentialFailureRateLimiter[*trackedRack](20*time.Millisecond, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.run(ctx) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	return f, calls
}

func watchedRack(name string, generation int64) *api.SGPURack {
	rack := &api.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")}}
	rack.Spec.NodeType = api.NodeTypeKWOK
	rack.Status.Delivery = &api.RackDelivery{Generation: generation}
	return rack
}

func nextBatch(t *testing.T, calls <-chan batchCall) batchCall {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a rack batch")
		return batchCall{}
	}
}

func rackResult(req state.RackRequest, revision int64) state.RackResult {
	return state.RackResult{Rack: &state.Rack{Hardware: state.Hardware{Rack: req.Object, RackDeliveryGeneration: req.MinRackDeliveryGeneration}, Nodes: []state.Slot{{Node: state.Object{Name: "worker", UID: "worker-uid"}, AssignmentRevision: revision}}}}
}

func TestFetchBatchesAndConcurrency(t *testing.T) {
	f, calls := testFetcher(t, batchOptions{100, 100 * time.Millisecond, 2})
	for i := range 205 {
		rack := watchedRack(fmt.Sprintf("rack-%03d", i), 1)
		f.observe(rack)
		f.observe(rack)
	}
	first, second := nextBatch(t, calls), nextBatch(t, calls)
	require.Len(t, first.racks, 100)
	require.Len(t, second.racks, 100)
	select {
	case <-calls:
		t.Fatal("third concurrent request")
	case <-time.After(150 * time.Millisecond):
	}
	seen := map[types.UID]bool{}
	respond := func(c batchCall) {
		results := make([]state.RackResult, 0, len(c.racks))
		for _, r := range c.racks {
			require.False(t, seen[r.UID])
			seen[r.UID] = true
			results = append(results, state.RackResult{Rack: &state.Rack{Hardware: state.Hardware{Rack: r.Object, RackDeliveryGeneration: 1}}})
		}
		c.reply <- batchReply{results: results}
	}
	respond(second)
	last := nextBatch(t, calls)
	require.Len(t, last.racks, 5)
	respond(last)
	respond(first)
	require.Len(t, seen, 205)
}

func TestFetchMakesProgressWhileGenerationAdvances(t *testing.T) {
	f, calls := testFetcher(t, batchOptions{1, time.Millisecond, 1})
	f.observe(watchedRack("rack", 1))
	call := nextBatch(t, calls)
	for generation := int64(1); generation <= 8; generation++ {
		require.Equal(t, generation, call.racks[0].MinRackDeliveryGeneration)
		f.observe(watchedRack("rack", generation+1))
		call.reply <- batchReply{results: []state.RackResult{rackResult(call.racks[0], 1)}}
		call = nextBatch(t, calls)
		node, ok := f.node("worker-uid")
		require.True(t, ok)
		require.Equal(t, generation, node.RackDeliveryGeneration)
	}
	// A response below the requested floor still cannot replace applied state.
	below := call.racks[0]
	below.MinRackDeliveryGeneration = 1
	call.reply <- batchReply{results: []state.RackResult{rackResult(below, 1)}}
	retry := nextBatch(t, calls)
	require.EqualValues(t, 9, retry.racks[0].MinRackDeliveryGeneration)
	node, _ := f.node("worker-uid")
	require.EqualValues(t, 8, node.RackDeliveryGeneration)
}

func TestWatchUpdatesRespectRetryAfter(t *testing.T) {
	f, calls := testFetcher(t, batchOptions{1, time.Millisecond, 1})
	f.observe(watchedRack("rack", 1))
	call := nextBatch(t, calls)
	// Cover both windows: an update during the HTTP request and another
	// after the retry has been scheduled.
	f.observe(watchedRack("rack", 2))
	start := time.Now()
	call.reply <- batchReply{code: http.StatusTooManyRequests, retryAfter: "1"}
	f.mu.RLock()
	item := f.owned["rack"]
	f.mu.RUnlock()
	require.Eventually(t, func() bool { return f.retries.NumRequeues(item) > 0 }, time.Second, time.Millisecond)
	f.observe(watchedRack("rack", 3))
	retry := nextBatch(t, calls)
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	require.EqualValues(t, 3, retry.racks[0].MinRackDeliveryGeneration)
}

func TestFetchRetryPreservesAppliedState(t *testing.T) {
	f, calls := testFetcher(t, batchOptions{100, 10 * time.Millisecond, 2})
	rack := watchedRack("rack", 1)
	f.observe(rack)
	c := nextBatch(t, calls)
	c.reply <- batchReply{results: []state.RackResult{rackResult(c.racks[0], 1)}}
	require.Eventually(t, func() bool { _, ok := f.node("worker-uid"); return ok }, time.Second, time.Millisecond)
	rack = watchedRack("rack", 2)
	f.observe(rack)
	for _, failure := range []batchReply{{code: 429}, {code: 503}, {results: []state.RackResult{{Reference: c.racks[0].Object, Status: state.NotReady}}}, {results: []state.RackResult{{Reference: c.racks[0].Object, Status: state.NotFound}}}} {
		c = nextBatch(t, calls)
		require.EqualValues(t, 2, c.racks[0].MinRackDeliveryGeneration)
		c.reply <- failure
		select {
		case <-calls:
			t.Fatal("a failed rack retried without backoff")
		case <-time.After(8 * time.Millisecond):
		}
		n, ok := f.node("worker-uid")
		require.True(t, ok)
		require.EqualValues(t, 1, n.RackDeliveryGeneration)
	}
	c = nextBatch(t, calls)
	c.reply <- batchReply{results: []state.RackResult{rackResult(c.racks[0], 2)}}
	require.Eventually(t, func() bool { n, _ := f.node("worker-uid"); return n.AssignmentRevision == 2 }, time.Second, time.Millisecond)
}

func TestInitialReadCanDeferOrDropARack(t *testing.T) {
	f, calls := testFetcher(t, batchOptions{2, time.Millisecond * 20, 1})
	unavailable, removed := watchedRack("waiting", 1), watchedRack("leaving", 1)
	f.observe(unavailable)
	f.observe(removed)
	call := nextBatch(t, calls)
	require.Len(t, call.racks, 2)
	ready := make(chan bool, 1)
	go func() { ready <- f.waitInitialReads(t.Context()) }()
	f.remove(removed)
	select {
	case <-ready:
		t.Fatal("initial load completed before the remaining rack replied")
	case <-time.After(10 * time.Millisecond):
	}
	results := make([]state.RackResult, 0, len(call.racks))
	for _, req := range call.racks {
		results = append(results, state.RackResult{Status: state.NotReady, Reference: req.Object})
	}
	call.reply <- batchReply{results: results}
	select {
	case ok := <-ready:
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("unavailable rack blocked initial Pod list")
	}
	retry := nextBatch(t, calls)
	require.Len(t, retry.racks, 1)
	require.Equal(t, unavailable.UID, retry.racks[0].UID)
}

func TestFetchDiscardsSupersededOwnershipAndRevision(t *testing.T) {
	f, calls := testFetcher(t, batchOptions{1, time.Second, 2})
	rack := watchedRack("rack", 7)
	f.observe(rack)
	old := nextBatch(t, calls)
	f.remove(cache.DeletedFinalStateUnknown{Key: rack.Name, Obj: rack})
	f.observe(rack)
	current := nextBatch(t, calls)
	current.reply <- batchReply{results: []state.RackResult{rackResult(current.racks[0], 3)}}
	require.Eventually(t, func() bool { n, _ := f.node("worker-uid"); return n.AssignmentRevision == 3 }, time.Second, time.Millisecond)
	old.reply <- batchReply{results: []state.RackResult{rackResult(old.racks[0], 99)}}
	other := watchedRack("replacement", 1)
	f.observe(other)
	moved := nextBatch(t, calls)
	moved.reply <- batchReply{results: []state.RackResult{rackResult(moved.racks[0], 4)}}
	require.Eventually(t, func() bool {
		n, _ := f.node("worker-uid")
		return n.AssignmentRevision == 4 && n.RackDeliveryGeneration == 1
	}, time.Second, time.Millisecond)
	rack = watchedRack("rack", 8)
	f.observe(rack)
	delayed := nextBatch(t, calls)
	delayed.reply <- batchReply{results: []state.RackResult{rackResult(delayed.racks[0], 3)}}
	// Reusing a rack name invalidates the previous UID and any delayed delete.
	recreated := rack.DeepCopy()
	recreated.UID = "new-uid"
	f.observe(recreated)
	f.remove(rack)
	c := nextBatch(t, calls)
	require.Equal(t, recreated.UID, c.racks[0].UID)
	n, ok := f.node("worker-uid")
	require.True(t, ok)
	require.EqualValues(t, 4, n.AssignmentRevision)
}
