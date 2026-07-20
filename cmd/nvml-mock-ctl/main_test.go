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

func TestCLI_TempWritesStaticAndDynamic(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "temp", "--gpu", "2", "85"); c != 0 {
		t.Fatalf("temp exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	s := string(data)
	for _, want := range []string{"temperature_gpu_c: 85", "base_c: 85", "ramp_c: 0", "variance_c: 0"} {
		if !strings.Contains(s, want) {
			t.Fatalf("overlay missing %q:\n%s", want, s)
		}
	}
}

func TestCLI_PowerConvertsWattsToMilliwatts(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "power", "--gpu", "all", "350"); c != 0 {
		t.Fatalf("power exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	s := string(data)
	if !strings.Contains(s, "current_draw_mw: 350000") || !strings.Contains(s, "base_mw: 350000") {
		t.Fatalf("power overlay did not convert watts->mW:\n%s", s)
	}
}

func TestCLI_FanForcesCount(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "fan", "--gpu", "0", "60"); c != 0 {
		t.Fatalf("fan exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	s := string(data)
	if !strings.Contains(s, "count: 1") || !strings.Contains(s, `speed_percent: "60"`) {
		t.Fatalf("fan overlay missing forced count / string speed:\n%s", s)
	}
}

func TestCLI_UtilWritesStaticAndDisablesDynamic(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "util", "--gpu", "0", "90"); c != 0 {
		t.Fatalf("util exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	s := string(data)
	for _, want := range []string{"gpu: 90", "memory: 90", "utilization: null"} {
		if !strings.Contains(s, want) {
			t.Fatalf("util overlay missing %q:\n%s", want, s)
		}
	}
}

func TestCLI_ClocksPinsSMAndGraphics(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "clocks", "--gpu", "all", "1980"); c != 0 {
		t.Fatalf("clocks exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	s := string(data)
	if !strings.Contains(s, "graphics_current: 1980") || !strings.Contains(s, "sm_current: 1980") {
		t.Fatalf("clocks overlay missing pinned clocks:\n%s", s)
	}
}

func TestCLI_ThrottleSetsReason(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "throttle", "--gpu", "0", "thermal"); c != 0 {
		t.Fatalf("throttle exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	s := string(data)
	if !strings.Contains(s, "hw_thermal_slowdown: true") {
		t.Fatalf("throttle overlay missing thermal reason:\n%s", s)
	}
	if !strings.Contains(s, "sw_power_cap: false") {
		t.Fatalf("throttle overlay should write authoritative false flags:\n%s", s)
	}
}

func TestCLI_PStatePins(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "pstate", "--gpu", "0", "8"); c != 0 {
		t.Fatalf("pstate exited %d: %s", c, e)
	}
	data, _ := os.ReadFile(overlay)
	if !strings.Contains(string(data), "performance_state: P8") {
		t.Fatalf("pstate overlay missing P8:\n%s", data)
	}
}

func TestCLI_ConvenienceArgValidation(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	cases := [][]string{
		{"temp", "--gpu", "0"},                        // missing value
		{"temp", "--gpu", "0", "hot"},                 // non-integer
		{"fan", "--gpu", "0", "150"},                  // out of range
		{"power", "--gpu", "0", "-5"},                 // negative watts
		{"power", "--gpu", "0", "1", "2"},             // too many values
		{"util", "--gpu", "0", "150"},                 // out of range
		{"pstate", "--gpu", "0", "16"},                // out of range
		{"throttle", "--gpu", "0"},                    // missing reason
		{"throttle", "--gpu", "0", "nope"},            // unknown reason
		{"throttle", "--gpu", "0", "none", "thermal"}, // none + reason
	}
	for _, args := range cases {
		if _, _, code := runCLI(t, overlay, args...); code != 2 {
			t.Fatalf("args %v exit = %d, want 2", args, code)
		}
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
