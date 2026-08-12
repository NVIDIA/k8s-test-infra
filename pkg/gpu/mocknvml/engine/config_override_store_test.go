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

	"github.com/stretchr/testify/require"
)

func TestConfigOverridePathFor_SiblingDefault(t *testing.T) {
	t.Setenv("MOCK_NVML_OVERRIDES", "")
	require.Equal(t, "/x/config/overrides.yaml", ConfigOverridePathFor("/x/config/config.yaml"))
}

func TestConfigOverridePathFor_EnvWins(t *testing.T) {
	t.Setenv("MOCK_NVML_OVERRIDES", "/custom/o.yaml")
	require.Equal(t, "/custom/o.yaml", ConfigOverridePathFor("/x/config/config.yaml"))
}

func TestConfigOverrideStore_GenBumpsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	now := time.Unix(0, 0)
	s := newConfigOverrideStoreAt(func() string { return path }, func() time.Time { return now })

	// Absent file: gen 0, nil doc.
	gen, doc := s.snapshot()
	require.Zero(t, gen, "absent config override gen")
	require.Nil(t, doc, "absent config override doc")

	// Write a file; TTL not elapsed yet -> still cached as absent.
	require.NoError(t, os.WriteFile(path, []byte("all:\n  failure:\n    mode: lost\n"), 0o644))
	gen, _ = s.snapshot()
	require.Zero(t, gen, "within TTL gen should stay 0")

	// Advance beyond TTL -> re-read, gen bumps, doc parsed.
	now = now.Add(2 * time.Second)
	gen, doc = s.snapshot()
	require.Equal(t, uint64(1), gen, "after change gen")
	require.NotNil(t, doc, "after change doc")
	require.Equal(t, "lost", doc.All["failure"].(map[string]any)["mode"], "parsed wrong")

	// No change -> gen stable across TTL windows.
	now = now.Add(2 * time.Second)
	gen, _ = s.snapshot()
	require.Equal(t, uint64(1), gen, "unchanged file should keep gen=1")

	// Remove file -> gen bumps again, doc nil.
	require.NoError(t, os.Remove(path))
	now = now.Add(2 * time.Second)
	gen, doc = s.snapshot()
	require.Equal(t, uint64(2), gen, "after removal gen")
	require.Nil(t, doc, "after removal doc")
}
