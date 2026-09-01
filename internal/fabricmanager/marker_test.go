// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabricmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func exists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(MarkerPath(dir))
	return err == nil
}

func TestMarkerLifecycle(t *testing.T) {
	dir := t.TempDir()
	require.False(t, exists(t, dir), "a fresh state dir is not ready")

	require.NoError(t, WriteReady(dir))
	require.True(t, exists(t, dir))

	// The daemon rewrites on a timer, so writing over an existing marker must
	// not error.
	require.NoError(t, WriteReady(dir))
	require.True(t, exists(t, dir))

	require.NoError(t, RemoveReady(dir))
	require.False(t, exists(t, dir))

	// Shutdown and stale-marker cleanup both run against possibly-partial
	// state, so removing what is not there is not an error.
	require.NoError(t, RemoveReady(dir))
}

// Stage creates the directory, but the daemon must not depend on that having
// happened: it writes into a state dir a restart may have removed.
func TestWriteReadyCreatesStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "fabric-state")
	require.NoError(t, WriteReady(dir))
	require.True(t, exists(t, dir))
}
