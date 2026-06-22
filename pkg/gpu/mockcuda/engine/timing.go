// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"sync"
	"time"
)

// timing holds stream/event state. Streams are opaque tokens; events capture a
// host monotonic timestamp on Record so ElapsedTime returns real wall-clock.
type timing struct {
	mu      sync.Mutex
	nextID  uint64
	streams map[uint64]struct{}
	events  map[uint64]*eventState
}

type eventState struct {
	recorded bool
	at       time.Time
}

func newTiming() *timing {
	return &timing{nextID: 1, streams: make(map[uint64]struct{}), events: make(map[uint64]*eventState)}
}

// StreamCreate returns a new opaque stream id (>0).
func (e *Engine) StreamCreate() uint64 {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextID
	t.nextID++
	t.streams[id] = struct{}{}
	return id
}

// StreamSynchronize is a no-op for a known stream.
func (e *Engine) StreamSynchronize(id uint64) CudaError {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.streams[id]; !ok {
		return CudaErrorInvalidValue
	}
	return CudaSuccess
}

// StreamDestroy removes a known stream.
func (e *Engine) StreamDestroy(id uint64) CudaError {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.streams[id]; !ok {
		return CudaErrorInvalidValue
	}
	delete(t.streams, id)
	return CudaSuccess
}

// EventCreate returns a new opaque event id (>0).
func (e *Engine) EventCreate() uint64 {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextID
	t.nextID++
	t.events[id] = &eventState{}
	return id
}

// EventRecord stamps the event with the current host time.
func (e *Engine) EventRecord(id uint64) CudaError {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	ev, ok := t.events[id]
	if !ok {
		return CudaErrorInvalidValue
	}
	ev.recorded = true
	ev.at = time.Now()
	return CudaSuccess
}

// EventSynchronize is a no-op for a known event.
func (e *Engine) EventSynchronize(id uint64) CudaError {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.events[id]; !ok {
		return CudaErrorInvalidValue
	}
	return CudaSuccess
}

// EventDestroy removes a known event.
func (e *Engine) EventDestroy(id uint64) CudaError {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.events[id]; !ok {
		return CudaErrorInvalidValue
	}
	delete(t.events, id)
	return CudaSuccess
}

// EventElapsedTime returns stop-start in milliseconds. Both events must have
// been recorded.
func (e *Engine) EventElapsedTime(start, stop uint64) (float64, CudaError) {
	t := e.timingState()
	t.mu.Lock()
	defer t.mu.Unlock()
	a, okA := t.events[start]
	b, okB := t.events[stop]
	if !okA || !okB {
		return 0, CudaErrorInvalidValue
	}
	if !a.recorded || !b.recorded {
		return 0, CudaErrorNotReady
	}
	return float64(b.at.Sub(a.at)) / float64(time.Millisecond), CudaSuccess
}
