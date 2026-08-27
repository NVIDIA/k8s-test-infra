// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

func TestMknod_CreatesChardev(t *testing.T) {
	skipUnlessRootLinux(t)

	h := New(t.TempDir())
	path := filepath.Join(h.Root, "test-chardev")

	require.NoError(t, h.Mknod(path, 195, 0))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.EqualValues(t, 0o666, fi.Mode().Perm())
}

func TestMknod_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)

	h := New(t.TempDir())
	path := filepath.Join(h.Root, "test-chardev")

	require.NoError(t, h.Mknod(path, 195, 0))
	require.NoError(t, h.Mknod(path, 195, 0), "second Mknod must not error")
}

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
