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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventElapsedMeasuresWallClock(t *testing.T) {
	e := NewEngine()
	start := e.EventCreate()
	require.Equal(t, CudaSuccess, e.EventRecord(start))
	time.Sleep(5 * time.Millisecond)
	stop := e.EventCreate()
	require.Equal(t, CudaSuccess, e.EventRecord(stop))
	ms, err := e.EventElapsedTime(start, stop)
	require.Equal(t, CudaSuccess, err)
	require.GreaterOrEqual(t, ms, 4.0)
	require.LessOrEqual(t, ms, 50.0)
}

func TestEventElapsedUnrecorded(t *testing.T) {
	e := NewEngine()
	a := e.EventCreate()
	b := e.EventCreate()
	_, err := e.EventElapsedTime(a, b)
	require.Equal(t, CudaErrorNotReady, err)
}

func TestStreamLifecycle(t *testing.T) {
	e := NewEngine()
	s := e.StreamCreate()
	require.Equal(t, CudaSuccess, e.StreamSynchronize(s))
	require.Equal(t, CudaSuccess, e.StreamDestroy(s))
	require.Equal(t, CudaErrorInvalidValue, e.StreamSynchronize(s))
}

func TestTimingConcurrentCreate(t *testing.T) {
	e := NewEngine()
	const goroutines = 8
	const perG = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				_ = e.StreamCreate()
				_ = e.EventCreate()
			}
		}()
	}
	wg.Wait()
	// All ids unique & monotonic counter advanced by exactly goroutines*perG*2.
	tm := e.timingState()
	tm.mu.Lock()
	defer tm.mu.Unlock()
	require.Len(t, tm.streams, goroutines*perG)
	require.Len(t, tm.events, goroutines*perG)
}
