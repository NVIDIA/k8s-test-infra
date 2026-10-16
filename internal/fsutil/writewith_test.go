// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteWithReplacesInOneStep(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sub", "file")
	require.NoError(t, WriteWith(path, 0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, "new")

		return err
	}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	boom := errors.New("boom")
	require.ErrorIs(t, WriteWith(path, 0o644, func(io.Writer) error { return boom }), boom)

	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(data), "a failed fill must leave the old file in place")
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "a failed fill must not leave its temp file behind")
}
