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
	"sync"
	"time"
)

const defaultOverlayTTL = time.Second

func overlayTTL() time.Duration {
	if v := os.Getenv("MOCK_NVML_OVERLAY_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultOverlayTTL
}

// overlayStore reads the overrides file at most once per TTL and exposes a
// monotonic generation that bumps whenever the file's observable state
// (absent / mtime / size) changes. Devices compare this generation to decide
// when to recompute their effective config, keeping the hot path allocation-
// and IO-free between changes. Modeled on fabricReadinessCache.
type overlayStore struct {
	mu       sync.Mutex
	checked  time.Time
	gen      uint64
	doc      *OverlayDoc
	lastMod  time.Time
	lastSize int64
	present  bool

	now    func() time.Time
	pathFn func() string
	ttl    time.Duration
}

func newOverlayStore() *overlayStore {
	return newOverlayStoreAt(resolveOverlayPath, time.Now)
}

func newOverlayStoreAt(pathFn func() string, now func() time.Time) *overlayStore {
	return &overlayStore{now: now, pathFn: pathFn, ttl: overlayTTL()}
}

// resolveOverlayPath derives the overlay path from the same resolution the
// engine uses for config. It is cheap and only called on cache misses.
func resolveOverlayPath() string {
	configPath := os.Getenv("MOCK_NVML_CONFIG")
	if configPath == "" {
		configPath = discoverConfigPath()
	}
	return OverlayPathFor(configPath)
}

func (s *overlayStore) snapshot() (uint64, *OverlayDoc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !s.checked.IsZero() && now.Sub(s.checked) < s.ttl {
		return s.gen, s.doc
	}
	s.checked = now

	path := s.pathFn()
	if path == "" {
		s.transition(false, time.Time{}, 0, nil)
		return s.gen, s.doc
	}

	fi, err := os.Stat(path)
	if err != nil {
		s.transition(false, time.Time{}, 0, nil)
		return s.gen, s.doc
	}

	// Unchanged file: no re-parse, no gen bump.
	if s.present && fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize {
		return s.gen, s.doc
	}

	data, err := os.ReadFile(path)
	if err != nil {
		s.transition(false, time.Time{}, 0, nil)
		return s.gen, s.doc
	}
	doc, err := ParseOverlay(data)
	if err != nil {
		warnLog("Failed to parse overrides %s: %v\n", path, err)
		// Keep the last good doc but do not bump gen on parse errors.
		return s.gen, s.doc
	}
	s.transition(true, fi.ModTime(), fi.Size(), doc)
	return s.gen, s.doc
}

// transition records new observed state and bumps gen when the effective
// content changed (presence flip or new mtime/size while present).
func (s *overlayStore) transition(present bool, mod time.Time, size int64, doc *OverlayDoc) {
	changed := present != s.present
	if present && s.present && (!mod.Equal(s.lastMod) || size != s.lastSize) {
		changed = true
	}
	s.present = present
	s.lastMod = mod
	s.lastSize = size
	s.doc = doc
	if changed {
		s.gen++
	}
}

var overlays = newOverlayStore()

func resetOverlayStoreForTesting() {
	overlays = newOverlayStore()
}
