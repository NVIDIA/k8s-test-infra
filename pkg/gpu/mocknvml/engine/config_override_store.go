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
	"sync/atomic"
	"time"
)

const defaultConfigOverrideTTL = time.Second

func configOverrideTTL() time.Duration {
	if v := os.Getenv("MOCK_NVML_OVERRIDES_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultConfigOverrideTTL
}

// configOverrideStore reads the overrides file at most once per TTL and exposes a
// monotonic generation that bumps whenever the file's observable state
// (absent / mtime / size) changes. Devices compare this generation to decide
// when to recompute their effective config, keeping the hot path allocation-
// and IO-free between changes. Modeled on fabricReadinessCache.
type configOverrideStore struct {
	mu       sync.Mutex
	checked  time.Time
	gen      uint64
	doc      *ConfigOverrideDoc
	lastMod  time.Time
	lastSize int64
	present  bool

	// Lock-free fast path. Within the TTL window snapshot() returns the last
	// published generation/doc without taking mu, avoiding contention when
	// many devices poll concurrently. These are published under mu on the way
	// out of the slow path, with checkedNanos stored LAST so a reader that
	// observes a fresh timestamp also observes the matching gen+doc. A
	// checkedNanos value of 0 is the "never checked" sentinel that forces the
	// first call down the mutex-guarded slow path.
	checkedNanos atomic.Int64
	genAtomic    atomic.Uint64
	docAtomic    atomic.Pointer[ConfigOverrideDoc]

	now    func() time.Time
	pathFn func() string
	ttl    time.Duration
}

func newConfigOverrideStore() *configOverrideStore {
	return newConfigOverrideStoreAt(resolveConfigOverridePath, time.Now)
}

func newConfigOverrideStoreAt(pathFn func() string, now func() time.Time) *configOverrideStore {
	return &configOverrideStore{now: now, pathFn: pathFn, ttl: configOverrideTTL()}
}

// resolveConfigOverridePath derives the config override path from the same resolution the
// engine uses for config. It is cheap and only called on cache misses.
func resolveConfigOverridePath() string {
	configPath := os.Getenv("MOCK_NVML_CONFIG")
	if configPath == "" {
		configPath = discoverConfigPath()
	}
	return ConfigOverridePathFor(configPath)
}

func (s *configOverrideStore) snapshot() (uint64, *ConfigOverrideDoc) {
	now := s.now()

	// Lock-free fast path: within the TTL window return the last published
	// generation/doc without touching the mutex. checkedNanos == 0 means
	// "never checked" and always falls through to the slow path so the first
	// call performs a real stat/read under the mutex.
	if checked := s.checkedNanos.Load(); checked != 0 && now.UnixNano()-checked < int64(s.ttl) {
		return s.genAtomic.Load(), s.docAtomic.Load()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Publish the mutex-guarded result to the lock-free fast path on the way
	// out (for every slow-path exit). checkedNanos is stored LAST so a
	// concurrent reader that sees the fresh timestamp also sees the matching
	// gen+doc.
	defer func() {
		s.docAtomic.Store(s.doc)
		s.genAtomic.Store(s.gen)
		s.checkedNanos.Store(s.checked.UnixNano())
	}()

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
	doc, err := ParseConfigOverride(data)
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
func (s *configOverrideStore) transition(present bool, mod time.Time, size int64, doc *ConfigOverrideDoc) {
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

var configOverrides = newConfigOverrideStore()

func resetConfigOverrideStoreForTesting() {
	configOverrides = newConfigOverrideStore()
}
