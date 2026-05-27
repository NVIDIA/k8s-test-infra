// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package counters

import (
	"sync"
	"testing"
	"time"
)

func TestEpochs_DefaultsToStart(t *testing.T) {
	start := time.Now()
	e := NewEpochs(start)
	if got := e.Elapsed(0, start.Add(7*time.Second)); got != 7*time.Second {
		t.Fatalf("elapsed = %v, want 7s", got)
	}
}

func TestEpochs_ResetMovesBaseline(t *testing.T) {
	start := time.Now()
	e := NewEpochs(start)
	e.Reset(3, start.Add(10*time.Second))
	if got := e.Elapsed(3, start.Add(12*time.Second)); got != 2*time.Second {
		t.Fatalf("post-reset elapsed = %v, want 2s", got)
	}
	if got := e.Elapsed(0, start.Add(12*time.Second)); got != 12*time.Second {
		t.Fatalf("other-ca elapsed = %v, want 12s", got)
	}
}

func TestEpochs_NegativeElapsedClamped(t *testing.T) {
	start := time.Now()
	e := NewEpochs(start)
	if got := e.Elapsed(0, start.Add(-1*time.Second)); got != 0 {
		t.Fatalf("clock skew elapsed = %v, want 0", got)
	}
}

func TestEpochs_ConcurrentSafe(t *testing.T) {
	start := time.Now()
	e := NewEpochs(start)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				e.Reset(idx, time.Now())
				_ = e.Elapsed(idx, time.Now())
			}
		}(i)
	}
	wg.Wait()
}
