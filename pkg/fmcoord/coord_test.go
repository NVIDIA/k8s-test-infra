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
)

func TestReadinessLifecycle(t *testing.T) {
	dir := t.TempDir()

	if IsReady(dir) {
		t.Fatal("fresh state dir should not be ready")
	}

	if err := WriteReady(dir); err != nil {
		t.Fatalf("WriteReady: %v", err)
	}
	if !IsReady(dir) {
		t.Fatal("after WriteReady the marker should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, ReadyMarker)); err != nil {
		t.Fatalf("marker file missing: %v", err)
	}

	// WriteReady is idempotent.
	if err := WriteReady(dir); err != nil {
		t.Fatalf("second WriteReady: %v", err)
	}

	if err := RemoveReady(dir); err != nil {
		t.Fatalf("RemoveReady: %v", err)
	}
	if IsReady(dir) {
		t.Fatal("after RemoveReady the marker should be gone")
	}
	// RemoveReady on a missing marker is a no-op.
	if err := RemoveReady(dir); err != nil {
		t.Fatalf("RemoveReady on missing marker should be nil: %v", err)
	}
}

func TestWriteReadyCreatesStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "fabric-state")
	if err := WriteReady(dir); err != nil {
		t.Fatalf("WriteReady should create the dir: %v", err)
	}
	if !IsReady(dir) {
		t.Fatal("marker should exist after creating nested dir")
	}
}

func TestStateDirEnvOverride(t *testing.T) {
	t.Setenv(EnvStateDir, "/custom/fabric-state")
	if got := StateDir(); got != "/custom/fabric-state" {
		t.Errorf("StateDir with env: got %q, want /custom/fabric-state", got)
	}
	if err := os.Unsetenv(EnvStateDir); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	if got := StateDir(); got != DefaultStateDir {
		t.Errorf("StateDir default: got %q, want %q", got, DefaultStateDir)
	}
}
