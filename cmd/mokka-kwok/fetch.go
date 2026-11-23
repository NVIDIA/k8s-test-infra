// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/util/workqueue"
)

type batchOptions struct {
	size    int
	window  time.Duration
	workers int
}

type rackFetcher struct {
	*rackViews
	queue workqueue.TypedDelayingInterface[*trackedRack]
	// Use the same per-rack counter for both server-directed and local retries.
	retries  workqueue.TypedRateLimiter[*trackedRack]
	endpoint string
	client   *http.Client
	options  batchOptions
}

func newRackFetcher(c config, views *rackViews) *rackFetcher {
	retries := workqueue.NewTypedItemExponentialFailureRateLimiter[*trackedRack](time.Second, time.Minute)
	return &rackFetcher{
		rackViews: views, options: c.batch,
		queue: workqueue.NewTypedDelayingQueue[*trackedRack](), retries: retries,
		endpoint: c.controlPlane + "/v1alpha1/racks/state:batchGet",
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *rackFetcher) run(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	items := make(chan *trackedRack)
	batches := make(chan []*trackedRack)
	stop := context.AfterFunc(ctx, f.queue.ShutDown)
	defer stop()
	group.Go(func() error {
		defer close(items)
		for {
			item, shutdown := f.queue.Get()
			if shutdown {
				return nil
			}
			select {
			case items <- item:
			case <-ctx.Done():
				return nil
			}
		}
	})
	group.Go(func() error { defer close(batches); f.collect(ctx, items, batches); return nil })
	for range f.options.workers {
		group.Go(func() error {
			for batch := range batches {
				f.fetch(ctx, batch)
			}
			return nil
		})
	}
	return group.Wait()
}

func (f *rackFetcher) collect(ctx context.Context, items <-chan *trackedRack, batches chan<- []*trackedRack) {
	for first := range items {
		batch := []*trackedRack{first}
		deadline := time.NewTimer(f.options.window)
	collect:
		for len(batch) < f.options.size {
			select {
			case item, ok := <-items:
				if !ok {
					deadline.Stop()
					return
				}
				batch = append(batch, item)
			case <-deadline.C:
				break collect
			}
		}
		deadline.Stop()
		select {
		case batches <- batch:
		case <-ctx.Done():
			return
		}
	}
}

func (f *rackFetcher) fetch(ctx context.Context, items []*trackedRack) {
	active, requests := f.pending(items)
	if len(active) == 0 {
		return
	}
	results, delay, err := f.batchGet(ctx, requests)
	if err != nil && ctx.Err() == nil {
		zap.L().Warn("Fetch KWOK rack state", zap.Error(err), zap.Int("racks", len(active)))
	}
	for i, item := range active {
		var result state.RackResult
		if err == nil {
			result = results[i]
		}
		f.finish(item, requests[i].MinRackDeliveryGeneration, result, delay)
	}
}

// Only the worker schedules follow-up work while a rack is pending. Watch
// updates can advance wanted without shortening a server or local backoff.
func (f *rackFetcher) finish(item *trackedRack, requested int64, result state.RackResult, delay time.Duration) {
	defer f.queue.Done(item)
	accepted := f.accept(item, requested, result)
	_, pending := f.request(item)
	if !accepted && pending {
		f.queue.AddAfter(item, max(f.retries.When(item), delay))
		return
	}
	f.retries.Forget(item)
	if pending {
		f.queue.Add(item)
	}
}

func (f *rackFetcher) pending(items []*trackedRack) ([]*trackedRack, []state.RackRequest) {
	requests := make([]state.RackRequest, 0, len(items))
	active := make([]*trackedRack, 0, len(items))
	for _, item := range items {
		request, needed := f.request(item)
		if needed {
			active = append(active, item)
			requests = append(requests, request)
		} else {
			f.retries.Forget(item)
			f.queue.Done(item)
		}
	}
	return active, requests
}

func (f *rackFetcher) batchGet(ctx context.Context, racks []state.RackRequest) ([]state.RackResult, time.Duration, error) {
	body, err := json.Marshal(state.BatchRequest{Racks: racks})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		seconds, _ := strconv.ParseUint(resp.Header.Get("Retry-After"), 10, 31)
		return nil, time.Duration(seconds) * time.Second, fmt.Errorf("rack batch returned %s", resp.Status)
	}
	var batch state.BatchResponse
	if err = json.NewDecoder(resp.Body).Decode(&batch); err != nil {
		return nil, 0, err
	}
	if batch.APIVersion != state.Version || len(batch.Racks) != len(racks) {
		return nil, 0, fmt.Errorf("invalid rack batch version %q or result count %d", batch.APIVersion, len(batch.Racks))
	}
	for i, result := range batch.Racks {
		if result.Reference != racks[i].Object {
			return nil, 0, fmt.Errorf("unexpected rack identity at batch index %d", i)
		}
	}
	return batch.Racks, 0, nil
}
