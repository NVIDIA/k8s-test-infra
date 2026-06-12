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

	"github.com/stretchr/testify/require"
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
	require.Equal(t, FabricStateCompleted, resolveFabricState("auto"), "auto with coupling disabled")
	// A static state is unaffected by coupling.
	require.Equal(t, FabricStateNotStarted, resolveFabricState("not_started"), "static not_started")
}

// TestFabricReadiness_CoupledMarker verifies the auto->ready transition
// reads the marker only when coupling is active.
func TestFabricReadiness_CoupledMarker(t *testing.T) {
	t.Setenv(EnvFabricStateDir, "/run/fabric-state")

	// Marker present -> COMPLETED.
	ready := newCache(time.Now, okStat)
	require.Equal(t, FabricStateCompleted, ready.state(), "marker present")

	// Marker absent -> IN_PROGRESS.
	notReady := newCache(time.Now, errStat)
	require.Equal(t, FabricStateInProgress, notReady.state(), "marker absent")
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

	require.Equal(t, FabricStateInProgress, c.state(), "first check should be not-ready")
	require.Equal(t, 1, calls, "first call stat count")
	// Second call within TTL must not stat again.
	require.Equal(t, FabricStateInProgress, c.state(), "cached check should still be not-ready")
	require.Equal(t, 1, calls, "cached call stat count (no re-stat within TTL)")
	// Advance past the TTL: a fresh stat is taken.
	now = now.Add(2 * fabricReadinessTTL)
	_ = c.state()
	require.Equal(t, 2, calls, "post-TTL stat count")
}
