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

package mockctl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResetOverrides(t *testing.T) {
	t.Parallel()

	t.Run("removes an existing document", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "overrides.yaml")
		require.NoError(t, WriteAtomic(path, &Doc{}))

		require.NoError(t, ResetOverrides(path))
		require.NoFileExists(t, path)
	})

	t.Run("is a no-op when the document is absent", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "overrides.yaml")

		require.NoError(t, ResetOverrides(path))
		require.NoFileExists(t, path)
	})

	t.Run("creates the parent directory it locks in", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "driver", "config")
		path := filepath.Join(dir, "overrides.yaml")

		require.NoError(t, ResetOverrides(path))
		require.DirExists(t, dir)
	})
}

func TestResetOverridesIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, WriteAtomic(path, &Doc{}))

	require.NoError(t, ResetOverrides(path))
	require.NoError(t, ResetOverrides(path))

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	// Only the lock file LockOverride creates should survive.
	require.Len(t, entries, 1)
	require.Equal(t, "overrides.yaml.lock", entries[0].Name())
}
