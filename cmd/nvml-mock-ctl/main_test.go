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
	"testing"

	"github.com/stretchr/testify/require"
)

func runCLI(t *testing.T, overlay string, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"--file", overlay}, args...)
	var out, errb bytes.Buffer
	code := run(full, &out, &errb)
	return out.String(), errb.String(), code
}

func readOverlay(t *testing.T, overlay string) string {
	t.Helper()
	data, err := os.ReadFile(overlay)
	require.NoError(t, err)
	return string(data)
}

func TestCLI_FailWritesOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, errStr, code := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)
	require.Contains(t, readOverlay(t, overlay), "ecc_uncorrectable")
}

func TestCLI_SetRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, _, code := runCLI(t, overlay, "set", "--gpu", "all", "bogus.field=1")
	require.NotEqual(t, 0, code, "expected non-zero exit for unknown field")
}

func TestCLI_TempWritesStaticAndDynamic(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "temp", "--gpu", "2", "85")
	require.Equalf(t, 0, c, "temp exited %d: %s", c, e)
	s := readOverlay(t, overlay)
	for _, want := range []string{"temperature_gpu_c: 85", "base_c: 85", "ramp_c: 0", "variance_c: 0"} {
		require.Containsf(t, s, want, "overlay missing %q", want)
	}
}

func TestCLI_PowerConvertsWattsToMilliwatts(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "power", "--gpu", "all", "350")
	require.Equalf(t, 0, c, "power exited %d: %s", c, e)
	s := readOverlay(t, overlay)
	require.Contains(t, s, "current_draw_mw: 350000")
	require.Contains(t, s, "base_mw: 350000")
}

func TestCLI_FanForcesCount(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "fan", "--gpu", "0", "60")
	require.Equalf(t, 0, c, "fan exited %d: %s", c, e)
	s := readOverlay(t, overlay)
	require.Contains(t, s, "count: 1")
	require.Contains(t, s, `speed_percent: "60"`)
}

func TestCLI_UtilWritesStaticAndDisablesDynamic(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "util", "--gpu", "0", "90")
	require.Equalf(t, 0, c, "util exited %d: %s", c, e)
	s := readOverlay(t, overlay)
	for _, want := range []string{"gpu: 90", "memory: 90", "utilization: null"} {
		require.Containsf(t, s, want, "util overlay missing %q", want)
	}
}

func TestCLI_ClocksPinsSMAndGraphics(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "clocks", "--gpu", "all", "1980")
	require.Equalf(t, 0, c, "clocks exited %d: %s", c, e)
	s := readOverlay(t, overlay)
	require.Contains(t, s, "graphics_current: 1980")
	require.Contains(t, s, "sm_current: 1980")
}

func TestCLI_ThrottleSetsReason(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "throttle", "--gpu", "0", "thermal")
	require.Equalf(t, 0, c, "throttle exited %d: %s", c, e)
	s := readOverlay(t, overlay)
	require.Contains(t, s, "hw_thermal_slowdown: true")
	require.Contains(t, s, "sw_power_cap: false", "throttle overlay should write authoritative false flags")
}

func TestCLI_PStatePins(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "pstate", "--gpu", "0", "8")
	require.Equalf(t, 0, c, "pstate exited %d: %s", c, e)
	require.Contains(t, readOverlay(t, overlay), "performance_state: P8")
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
		_, _, code := runCLI(t, overlay, args...)
		require.Equalf(t, 2, code, "args %v exit = %d, want 2", args, code)
	}
}

func TestCLI_StatusEmpty(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	out, _, code := runCLI(t, overlay, "status")
	require.Equal(t, 0, code, "status should succeed on absent overlay")
	require.Contains(t, out, "no active overrides")
}

func TestCLI_OverlayFileWorldReadable(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "lost")
	require.Equalf(t, 0, c, "fail command exited %d: %s", c, e)
	fi, err := os.Stat(overlay)
	require.NoError(t, err)
	require.Equalf(t, os.FileMode(0o644), fi.Mode().Perm(), "overlay mode = %o, want 0644", fi.Mode().Perm())
}

func TestCLI_StatusFilterByGPU(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "lost")
	require.Equalf(t, 0, c, "setup gpu 0: %s", e)
	_, e, c = runCLI(t, overlay, "fail", "--gpu", "1", "--mode", "ecc_uncorrectable")
	require.Equalf(t, 0, c, "setup gpu 1: %s", e)

	// Targeted status shows only the requested device's bucket.
	out, _, code := runCLI(t, overlay, "status", "--gpu", "0")
	require.Equalf(t, 0, code, "status --gpu 0 exited %d", code)
	require.Contains(t, out, "lost", "status --gpu 0 missing device 0 override")
	require.NotContains(t, out, "ecc_uncorrectable", "status --gpu 0 leaked device 1 override")

	// A device with no overrides reports so explicitly.
	out, _, code = runCLI(t, overlay, "status", "--gpu", "5")
	require.Equalf(t, 0, code, "status --gpu 5 exited %d", code)
	require.Contains(t, out, "no active overrides for gpu 5")

	// Non-integer index is a usage error.
	_, _, code = runCLI(t, overlay, "status", "--gpu", "all")
	require.Equalf(t, 2, code, "status --gpu all exit = %d, want 2", code)
}

func TestCLI_ResetGPU(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, overlay, "fail", "--gpu", "1", "--mode", "lost")
	require.Equalf(t, 0, c, "setup fail: %s", e)
	_, e, c = runCLI(t, overlay, "reset", "--gpu", "1")
	require.Equalf(t, 0, c, "reset: %s", e)
	require.NotContains(t, readOverlay(t, overlay), "lost", "reset did not remove device 1")
}
