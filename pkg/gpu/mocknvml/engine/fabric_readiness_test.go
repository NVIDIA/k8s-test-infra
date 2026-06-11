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
	"os"
	"testing"
	"time"
)

// newCache returns a fabricReadinessCache with injected clock/stat so the
// tests never touch the real filesystem or wall clock.
func newCache(now func() time.Time, stat func(string) (os.FileInfo, error)) *fabricReadinessCache {
	return &fabricReadinessCache{now: now, stat: stat}
}

func okStat(string) (os.FileInfo, error)  { return nil, nil }
func errStat(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

// TestResolveFabricState_AutoDisabled verifies that "auto" resolves to
// COMPLETED when coupling is off (no fabricmanager): no regression to the
// healthy single-node / ComputeDomain default.
func TestResolveFabricState_AutoDisabled(t *testing.T) {
	// Empty value == coupling off (the engine treats blank as "not set"),
	// and t.Setenv restores any ambient value after the test.
	t.Setenv(EnvFabricStateDir, "")
	if got := resolveFabricState("auto"); got != FabricStateCompleted {
		t.Errorf("auto with coupling disabled: got %d, want COMPLETED(%d)", got, FabricStateCompleted)
	}
	// A static state is unaffected by coupling.
	if got := resolveFabricState("not_started"); got != FabricStateNotStarted {
		t.Errorf("static not_started: got %d, want %d", got, FabricStateNotStarted)
	}
}

// TestFabricReadiness_CoupledMarker verifies the auto->ready transition
// reads the marker only when coupling is active.
func TestFabricReadiness_CoupledMarker(t *testing.T) {
	t.Setenv(EnvFabricStateDir, "/run/fabric-state")

	// Marker present -> COMPLETED.
	ready := newCache(time.Now, okStat)
	if got := ready.state(); got != FabricStateCompleted {
		t.Errorf("marker present: got %d, want COMPLETED(%d)", got, FabricStateCompleted)
	}

	// Marker absent -> IN_PROGRESS.
	notReady := newCache(time.Now, errStat)
	if got := notReady.state(); got != FabricStateInProgress {
		t.Errorf("marker absent: got %d, want IN_PROGRESS(%d)", got, FabricStateInProgress)
	}
}

// TestFabricReadiness_TTLCaching verifies the cache does not stat() on
// every call: within the TTL the first result is reused even after the
// underlying marker flips.
func TestFabricReadiness_TTLCaching(t *testing.T) {
	t.Setenv(EnvFabricStateDir, "/run/fabric-state")

	now := time.Unix(1000, 0)
	calls := 0
	stat := func(string) (os.FileInfo, error) {
		calls++
		return nil, os.ErrNotExist // not ready
	}
	c := newCache(func() time.Time { return now }, stat)

	if c.state() != FabricStateInProgress {
		t.Fatal("first check should be not-ready")
	}
	if calls != 1 {
		t.Fatalf("first call stat count: got %d, want 1", calls)
	}
	// Second call within TTL must not stat again.
	if c.state() != FabricStateInProgress {
		t.Fatal("cached check should still be not-ready")
	}
	if calls != 1 {
		t.Fatalf("cached call stat count: got %d, want 1 (no re-stat within TTL)", calls)
	}
	// Advance past the TTL: a fresh stat is taken.
	now = now.Add(2 * fabricReadinessTTL)
	_ = c.state()
	if calls != 2 {
		t.Fatalf("post-TTL stat count: got %d, want 2", calls)
	}
}
