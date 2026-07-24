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
	"path/filepath"
	"testing"
	"time"
)

func TestConfigOverridePathFor_SiblingDefault(t *testing.T) {
	t.Setenv("MOCK_NVML_OVERRIDES", "")
	got := ConfigOverridePathFor("/x/config/config.yaml")
	if got != "/x/config/overrides.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestConfigOverridePathFor_EnvWins(t *testing.T) {
	t.Setenv("MOCK_NVML_OVERRIDES", "/custom/o.yaml")
	if got := ConfigOverridePathFor("/x/config/config.yaml"); got != "/custom/o.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestConfigOverrideStore_GenBumpsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	now := time.Unix(0, 0)
	s := newConfigOverrideStoreAt(func() string { return path }, func() time.Time { return now })

	// Absent file: gen 0, nil doc.
	if gen, doc := s.snapshot(); gen != 0 || doc != nil {
		t.Fatalf("absent config override: gen=%d doc=%v", gen, doc)
	}

	// Write a file; TTL not elapsed yet -> still cached as absent.
	if err := os.WriteFile(path, []byte("all:\n  failure:\n    mode: lost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if gen, _ := s.snapshot(); gen != 0 {
		t.Fatalf("within TTL gen should stay 0, got %d", gen)
	}

	// Advance beyond TTL -> re-read, gen bumps, doc parsed.
	now = now.Add(2 * time.Second)
	gen, doc := s.snapshot()
	if gen != 1 || doc == nil {
		t.Fatalf("after change: gen=%d doc=%v", gen, doc)
	}
	if doc.All["failure"].(map[string]any)["mode"] != "lost" {
		t.Fatalf("parsed wrong: %+v", doc.All)
	}

	// No change -> gen stable across TTL windows.
	now = now.Add(2 * time.Second)
	if gen2, _ := s.snapshot(); gen2 != 1 {
		t.Fatalf("unchanged file should keep gen=1, got %d", gen2)
	}

	// Remove file -> gen bumps again, doc nil.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if gen3, doc3 := s.snapshot(); gen3 != 2 || doc3 != nil {
		t.Fatalf("after removal: gen=%d doc=%v", gen3, doc3)
	}
}
