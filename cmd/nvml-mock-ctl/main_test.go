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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, overlay string, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"--file", overlay}, args...)
	var out, errb bytes.Buffer
	code := run(full, &out, &errb)
	return out.String(), errb.String(), code
}

func TestCLI_FailWritesOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, errStr, code := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errStr)
	}
	data, _ := os.ReadFile(overlay)
	if !strings.Contains(string(data), "ecc_uncorrectable") {
		t.Fatalf("overlay missing mode: %s", data)
	}
}

func TestCLI_SetRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, _, code := runCLI(t, overlay, "set", "--gpu", "all", "bogus.field=1")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown field")
	}
}

func TestCLI_StatusEmpty(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	out, _, code := runCLI(t, overlay, "status")
	if code != 0 {
		t.Fatal("status should succeed on absent overlay")
	}
	if !strings.Contains(out, "no active overrides") {
		t.Fatalf("unexpected status: %s", out)
	}
}

func TestCLI_OverlayFileWorldReadable(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "lost"); c != 0 {
		t.Fatalf("fail command exited %d: %s", c, e)
	}
	fi, err := os.Stat(overlay)
	if err != nil {
		t.Fatalf("stat overlay: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Fatalf("overlay mode = %o, want 0644", perm)
	}
}

func TestCLI_StatusFilterByGPU(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "lost"); c != 0 {
		t.Fatalf("setup gpu 0: %s", e)
	}
	if _, e, c := runCLI(t, overlay, "fail", "--gpu", "1", "--mode", "ecc_uncorrectable"); c != 0 {
		t.Fatalf("setup gpu 1: %s", e)
	}

	// Targeted status shows only the requested device's bucket.
	out, _, code := runCLI(t, overlay, "status", "--gpu", "0")
	if code != 0 {
		t.Fatalf("status --gpu 0 exited %d", code)
	}
	if !strings.Contains(out, "lost") {
		t.Fatalf("status --gpu 0 missing device 0 override: %s", out)
	}
	if strings.Contains(out, "ecc_uncorrectable") {
		t.Fatalf("status --gpu 0 leaked device 1 override: %s", out)
	}

	// A device with no overrides reports so explicitly.
	out, _, code = runCLI(t, overlay, "status", "--gpu", "5")
	if code != 0 {
		t.Fatalf("status --gpu 5 exited %d", code)
	}
	if !strings.Contains(out, "no active overrides for gpu 5") {
		t.Fatalf("unexpected status for empty gpu: %s", out)
	}

	// Non-integer index is a usage error.
	if _, _, code := runCLI(t, overlay, "status", "--gpu", "all"); code != 2 {
		t.Fatalf("status --gpu all exit = %d, want 2", code)
	}
}

func TestCLI_ResetGPU(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "fail", "--gpu", "1", "--mode", "lost"); c != 0 {
		t.Fatalf("setup fail: %s", e)
	}
	if _, e, c := runCLI(t, overlay, "reset", "--gpu", "1"); c != 0 {
		t.Fatalf("reset: %s", e)
	}
	data, _ := os.ReadFile(overlay)
	if strings.Contains(string(data), "lost") {
		t.Fatalf("reset did not remove device 1: %s", data)
	}
}
