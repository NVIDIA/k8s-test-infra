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

package fmcoord

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadinessLifecycle(t *testing.T) {
	dir := t.TempDir()

	require.False(t, IsReady(dir), "fresh state dir should not be ready")

	require.NoError(t, WriteReady(dir), "WriteReady")
	require.True(t, IsReady(dir), "after WriteReady the marker should exist")
	_, err := os.Stat(filepath.Join(dir, ReadyMarker))
	require.NoError(t, err, "marker file missing")

	// WriteReady is idempotent.
	require.NoError(t, WriteReady(dir), "second WriteReady")

	require.NoError(t, RemoveReady(dir), "RemoveReady")
	require.False(t, IsReady(dir), "after RemoveReady the marker should be gone")
	// RemoveReady on a missing marker is a no-op.
	require.NoError(t, RemoveReady(dir), "RemoveReady on missing marker should be nil")
}

func TestWriteReadyCreatesStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "fabric-state")
	require.NoError(t, WriteReady(dir), "WriteReady should create the dir")
	require.True(t, IsReady(dir), "marker should exist after creating nested dir")
}

func TestStateDirEnvOverride(t *testing.T) {
	t.Setenv(EnvStateDir, "/custom/fabric-state")
	require.Equal(t, "/custom/fabric-state", StateDir(), "StateDir with env")
	require.NoError(t, os.Unsetenv(EnvStateDir), "unset env")
	require.Equal(t, DefaultStateDir, StateDir(), "StateDir default")
}
