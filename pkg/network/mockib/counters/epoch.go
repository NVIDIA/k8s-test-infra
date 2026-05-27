// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package counters

import (
	"sync"
	"time"
)

// Epochs tracks the per-CA reset baseline used by the Generator. When a
// PMA Set ClearCounters arrives, Reset(caIdx, now) advances the baseline
// so subsequent reads see counters restarted from zero. Until any reset,
// the baseline is the daemon start time.
type Epochs struct {
	mu    sync.Mutex
	start time.Time
	reset map[int]time.Time
}

// NewEpochs constructs an Epochs anchored at start.
func NewEpochs(start time.Time) *Epochs {
	return &Epochs{start: start, reset: make(map[int]time.Time)}
}

// Elapsed returns now - max(start, reset[caIdx]). Clamped to 0 if
// negative (defends against the unlikely case of clock skew between the
// writer and PMA paths).
func (e *Epochs) Elapsed(caIdx int, now time.Time) time.Duration {
	e.mu.Lock()
	base := e.start
	if r, ok := e.reset[caIdx]; ok && r.After(base) {
		base = r
	}
	e.mu.Unlock()
	d := now.Sub(base)
	if d < 0 {
		return 0
	}
	return d
}

// Reset advances the per-CA baseline. Subsequent Elapsed calls return
// now - r for that caIdx.
func (e *Epochs) Reset(caIdx int, now time.Time) {
	e.mu.Lock()
	e.reset[caIdx] = now
	e.mu.Unlock()
}
