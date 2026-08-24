// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostRemove(t *testing.T) {
	t.Run("existing file is removed", func(t *testing.T) {
		h := New(t.TempDir())
		p := filepath.Join(h.Root, "file.txt")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))

		require.NoError(t, h.Remove(p))
		_, err := os.Stat(p)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("absent path is not an error", func(t *testing.T) {
		h := New(t.TempDir())
		require.NoError(t, h.Remove(filepath.Join(h.Root, "does-not-exist")))
	})
}
