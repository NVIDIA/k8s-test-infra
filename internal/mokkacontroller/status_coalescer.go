// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"sync"
	"time"

	"k8s.io/client-go/util/workqueue"
)

type statusTimer interface {
	Stop() bool
}

type statusScheduler interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) statusTimer
}

type realStatusScheduler struct{}

func (realStatusScheduler) Now() time.Time { return time.Now() }

func (realStatusScheduler) AfterFunc(delay time.Duration, fn func()) statusTimer {
	return time.AfterFunc(delay, fn)
}

type statusCoalescer struct {
	mu        sync.Mutex
	queue     workqueue.TypedInterface[statusKey]
	debounce  time.Duration
	progress  time.Duration
	scheduler statusScheduler
	states    map[statusKey]*statusSchedule
	sequence  uint64
	stopped   bool
}

type statusSchedule struct {
	dirty         bool
	queued        bool
	inFlight      bool
	retryPending  bool
	timer         statusTimer
	timerSequence uint64
	burstStarted  time.Time
	lastDirty     time.Time
}

func newStatusCoalescer(
	queue workqueue.TypedInterface[statusKey],
	debounce time.Duration,
	progress time.Duration,
	scheduler statusScheduler,
) *statusCoalescer {
	return &statusCoalescer{
		queue: queue, debounce: debounce, progress: progress, scheduler: scheduler,
		states: make(map[statusKey]*statusSchedule),
	}
}

func (c *statusCoalescer) dirty(key statusKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if c.debounce == 0 {
		c.queue.Add(key)
		return
	}

	state := c.states[key]
	if state == nil {
		state = &statusSchedule{}
		c.states[key] = state
	}
	now := c.scheduler.Now()
	if !state.dirty {
		state.burstStarted = now
	}
	state.dirty = true
	state.lastDirty = now
	if !state.queued && !state.inFlight && !state.retryPending {
		c.rescheduleLocked(key, state)
	}
}

func (c *statusCoalescer) start(key statusKey) {
	if c.debounce == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	state := c.states[key]
	if state == nil {
		state = &statusSchedule{}
		c.states[key] = state
	}
	state.queued = false
	state.retryPending = false
	state.inFlight = true
}

func (c *statusCoalescer) finish(key statusKey, success bool) {
	if c.debounce == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	state := c.states[key]
	if state == nil {
		return
	}
	state.inFlight = false
	if !success {
		state.retryPending = true
		return
	}
	state.retryPending = false
	if !state.dirty {
		delete(c.states, key)
		return
	}
	if !state.queued {
		c.rescheduleLocked(key, state)
	}
}

func (c *statusCoalescer) rescheduleLocked(key statusKey, state *statusSchedule) {
	if state.timer != nil {
		state.timer.Stop()
	}
	now := c.scheduler.Now()
	deadline := state.lastDirty.Add(c.debounce)
	progressDeadline := state.burstStarted.Add(c.progress)
	if progressDeadline.Before(deadline) {
		deadline = progressDeadline
	}
	delay := deadline.Sub(now)
	if delay < 0 {
		delay = 0
	}
	c.sequence++
	sequence := c.sequence
	state.timerSequence = sequence
	state.timer = c.scheduler.AfterFunc(delay, func() {
		c.dispatch(key, sequence)
	})
}

func (c *statusCoalescer) dispatch(key statusKey, sequence uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	state := c.states[key]
	if state == nil || state.timerSequence != sequence {
		return
	}
	state.timer = nil
	if !state.dirty || state.queued || state.inFlight || state.retryPending {
		return
	}
	state.dirty = false
	state.queued = true
	state.burstStarted = time.Time{}
	state.lastDirty = time.Time{}
	// Holding the coalescer lock makes shutdown a barrier: no callback can add
	// work after shutdown has returned and the underlying queue is stopped.
	c.queue.Add(key)
}

func (c *statusCoalescer) shutdown() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	timers := make([]statusTimer, 0, len(c.states))
	for _, state := range c.states {
		if state.timer != nil {
			timers = append(timers, state.timer)
		}
	}
	c.states = nil
	c.mu.Unlock()

	for _, timer := range timers {
		timer.Stop()
	}
}
