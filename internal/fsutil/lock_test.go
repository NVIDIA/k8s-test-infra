// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fsutil

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLockSiblingExcludesASecondHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config", "overrides.yaml")
	unlock, err := LockSibling(path)
	require.NoError(t, err)
	require.FileExists(t, path+".lock")

	acquired := make(chan func())
	go func() {
		second, err := LockSibling(path)
		if err != nil {
			close(acquired)
			return
		}
		acquired <- second
	}()

	select {
	case <-acquired:
		t.Fatal("a second holder took the lock while the first held it")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()

	select {
	case second, ok := <-acquired:
		require.True(t, ok, "the second LockSibling failed")
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("the second holder never got the lock")
	}
}
