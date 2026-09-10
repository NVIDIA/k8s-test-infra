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

	"github.com/stretchr/testify/require"
)

func runCLI(t *testing.T, configOverride string, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"--file", configOverride}, args...)
	var out, errb bytes.Buffer
	code := run(full, &out, &errb)
	return out.String(), errb.String(), code
}

func readConfigOverride(t *testing.T, configOverride string) string {
	t.Helper()
	data, err := os.ReadFile(configOverride)
	require.NoError(t, err)
	return string(data)
}

func TestCLI_FailWritesConfigOverride(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, errStr, code := runCLI(t, configOverride, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)
	require.Contains(t, readConfigOverride(t, configOverride), "ecc_uncorrectable")
}

func TestCLI_SetRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, _, code := runCLI(t, configOverride, "set", "--gpu", "all", "bogus.field=1")
	require.NotEqual(t, 0, code, "expected non-zero exit for unknown field")
}

func TestCLI_TempWritesStaticAndDynamic(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "temp", "--gpu", "2", "85")
	require.Equalf(t, 0, c, "temp exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	for _, want := range []string{"temperature_gpu_c: 85", "base_c: 85", "ramp_c: 0", "variance_c: 0"} {
		require.Containsf(t, s, want, "configOverride missing %q", want)
	}
}

func TestCLI_PowerConvertsWattsToMilliwatts(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "power", "--gpu", "all", "350")
	require.Equalf(t, 0, c, "power exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "current_draw_mw: 350000")
	require.Contains(t, s, "base_mw: 350000")
}

func TestCLI_FanForcesCount(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "fan", "--gpu", "0", "60")
	require.Equalf(t, 0, c, "fan exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "count: 1")
	require.Contains(t, s, `speed_percent: "60"`)
}

func TestCLI_UtilWritesStaticAndDisablesDynamic(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "util", "--gpu", "0", "90")
	require.Equalf(t, 0, c, "util exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	for _, want := range []string{"gpu: 90", "memory: 90", "utilization: null"} {
		require.Containsf(t, s, want, "util configOverride missing %q", want)
	}
}

func TestCLI_ClocksPinsSMAndGraphics(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "clocks", "--gpu", "all", "1980")
	require.Equalf(t, 0, c, "clocks exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "graphics_current: 1980")
	require.Contains(t, s, "sm_current: 1980")
}

func TestCLI_ThrottleSetsReason(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "throttle", "--gpu", "0", "thermal")
	require.Equalf(t, 0, c, "throttle exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "hw_thermal_slowdown: true")
	require.Contains(t, s, "sw_power_cap: false", "throttle configOverride should write authoritative false flags")
}

func TestCLI_PStatePins(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "pstate", "--gpu", "0", "8")
	require.Equalf(t, 0, c, "pstate exited %d: %s", c, e)
	require.Contains(t, readConfigOverride(t, configOverride), "performance_state: P8")
}

func TestCLI_NVLinkErrorWritesRate(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "nvlink-error", "--gpu", "0", "250")
	require.Equalf(t, 0, c, "nvlink-error exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "nvlink_error")
	require.Contains(t, s, "rate: 250")
}

func TestCLI_NVLinkErrorWithLinks(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "nvlink-error", "--gpu", "1", "--links", "0,3,7", "100")
	require.Equalf(t, 0, c, "nvlink-error exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	for _, want := range []string{"rate: 100", "- 0", "- 3", "- 7"} {
		require.Containsf(t, s, want, "configOverride missing %q", want)
	}
}

func TestCLI_NVLinkErrorZeroHeals(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "nvlink-error", "--gpu", "0", "0")
	require.Equalf(t, 0, c, "nvlink-error exited %d: %s", c, e)
	require.Contains(t, readConfigOverride(t, configOverride), "rate: 0")
}

func TestCLI_SramECCWritesCounters(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "sram-ecc", "--gpu", "0", "--source", "sm", "--threshold-exceeded", "4")
	require.Equalf(t, 0, c, "sram-ecc exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	for _, want := range []string{"sram:", "uncorrectable_secded: 4", "sm: 4", "threshold_exceeded: true"} {
		require.Containsf(t, s, want, "configOverride missing %q", want)
	}
}

func TestCLI_SramECCZeroHeals(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "sram-ecc", "--gpu", "0", "0")
	require.Equalf(t, 0, c, "sram-ecc exited %d: %s", c, e)
	require.Contains(t, readConfigOverride(t, configOverride), "uncorrectable_secded: 0")
}

func TestCLI_FabricHealthDegradesOneCondition(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "fabric-health", "--gpu", "0", "route_unhealthy")
	require.Equalf(t, 0, c, "fabric-health exited %d: %s", c, e)
	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "route_unhealthy: true")
	require.Contains(t, s, "route_recovery: false", "fabric-health should write authoritative false conditions")
	require.Contains(t, s, "health_summary: auto", "the summary must follow the injected conditions")
}

func TestCLI_FabricHealthHealthyClears(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "fabric-health", "--gpu", "0", "route_unhealthy")
	require.Equalf(t, 0, c, "fabric-health exited %d: %s", c, e)
	_, e, c = runCLI(t, configOverride, "fabric-health", "--gpu", "0", "healthy")
	require.Equalf(t, 0, c, "fabric-health healthy exited %d: %s", c, e)
	require.Contains(t, readConfigOverride(t, configOverride), "route_unhealthy: false")
}

func TestCLI_ConvenienceArgValidation(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	cases := [][]string{
		{"temp", "--gpu", "0"},                                       // missing value
		{"temp", "--gpu", "0", "hot"},                                // non-integer
		{"fan", "--gpu", "0", "150"},                                 // out of range
		{"power", "--gpu", "0", "--", "-5"},                          // negative watts (-- so it reaches the guard, not flag.Parse)
		{"power", "--gpu", "0", "NaN"},                               // non-finite watts
		{"power", "--gpu", "0", "Inf"},                               // non-finite watts
		{"power", "--gpu", "0", "10000000"},                          // watts overflow guard
		{"power", "--gpu", "0", "1", "2"},                            // too many values
		{"util", "--gpu", "0", "150"},                                // out of range
		{"pstate", "--gpu", "0", "16"},                               // out of range
		{"throttle", "--gpu", "0"},                                   // missing reason
		{"throttle", "--gpu", "0", "nope"},                           // unknown reason
		{"throttle", "--gpu", "0", "none", "thermal"},                // none + reason
		{"nvlink-error", "--gpu", "0"},                               // missing rate
		{"nvlink-error", "--gpu", "0", "-5"},                         // negative rate (flag.Parse stops at -5 -> missing value)
		{"nvlink-error", "--gpu", "0", "2000000000"},                 // rate over cap
		{"nvlink-error", "--gpu", "0", "--links", "x", "1"},          // non-integer link id
		{"sram-ecc", "--gpu", "0"},                                   // missing count
		{"sram-ecc", "--gpu", "0", "--type", "nope", "1"},            // unknown error type
		{"sram-ecc", "--gpu", "0", "--source", "nope", "1"},          // unknown source
		{"fabric-health", "--gpu", "0"},                              // missing condition
		{"fabric-health", "--gpu", "0", "nope"},                      // unknown condition
		{"fabric-health", "--gpu", "0", "healthy", "route_recovery"}, // healthy + fault
		// The per-source breakdown only covers uncorrectable errors.
		{"sram-ecc", "--gpu", "0", "--type", "correctable", "--source", "sm", "1"},
	}
	for _, args := range cases {
		_, _, code := runCLI(t, configOverride, args...)
		require.Equalf(t, 2, code, "args %v exit = %d, want 2", args, code)
	}
}

func TestCLI_StatusEmpty(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	out, _, code := runCLI(t, configOverride, "status")
	require.Equal(t, 0, code, "status should succeed on absent configOverride")
	require.Contains(t, out, "no active overrides")
}

func TestCLI_ConfigOverrideFileWorldReadable(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "fail", "--gpu", "0", "--mode", "lost")
	require.Equalf(t, 0, c, "fail command exited %d: %s", c, e)
	fi, err := os.Stat(configOverride)
	require.NoError(t, err)
	require.Equalf(t, os.FileMode(0o644), fi.Mode().Perm(), "configOverride mode = %o, want 0644", fi.Mode().Perm())
}

func TestCLI_StatusFilterByGPU(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "fail", "--gpu", "0", "--mode", "lost")
	require.Equalf(t, 0, c, "setup gpu 0: %s", e)
	_, e, c = runCLI(t, configOverride, "fail", "--gpu", "1", "--mode", "ecc_uncorrectable")
	require.Equalf(t, 0, c, "setup gpu 1: %s", e)

	// Targeted status shows only the requested device's bucket.
	out, _, code := runCLI(t, configOverride, "status", "--gpu", "0")
	require.Equalf(t, 0, code, "status --gpu 0 exited %d", code)
	require.Contains(t, out, "lost", "status --gpu 0 missing device 0 override")
	require.NotContains(t, out, "ecc_uncorrectable", "status --gpu 0 leaked device 1 override")

	// A device with no overrides reports so explicitly.
	out, _, code = runCLI(t, configOverride, "status", "--gpu", "5")
	require.Equalf(t, 0, code, "status --gpu 5 exited %d", code)
	require.Contains(t, out, "no active overrides for gpu 5")

	// Non-integer index is a usage error.
	_, _, code = runCLI(t, configOverride, "status", "--gpu", "all")
	require.Equalf(t, 2, code, "status --gpu all exit = %d, want 2", code)
}

// The global flags are declared on the root command and inherited, so they
// still work on either side of the subcommand the way callers write them.
func TestCLI_GlobalFlagsWorkOnEitherSideOfTheCommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--file", "%s", "temp", "--gpu", "0", "85"},
		{"temp", "--gpu", "0", "--file", "%s", "85"},
		{"temp", "--gpu", "0", "85", "--file", "%s"},
	} {
		configOverride := filepath.Join(t.TempDir(), "overrides.yaml")
		full := make([]string, len(args))
		for i, a := range args {
			full[i] = strings.Replace(a, "%s", configOverride, 1)
		}
		var out, errb bytes.Buffer
		code := run(full, &out, &errb)
		require.Equalf(t, 0, code, "args %v exited %d: %s", args, code, errb.String())
		require.Contains(t, readConfigOverride(t, configOverride), "temperature_gpu_c: 85")
	}
}

// An explicitly empty target must not be read as "every device". Only reset
// applies to everything, and only when --gpu is absent altogether, so a
// mistyped `--gpu ""` has to fail rather than quietly hit the whole node.
func TestCLI_EmptyTargetIsRejectedNotBroadcast(t *testing.T) {
	t.Parallel()
	configOverride := filepath.Join(t.TempDir(), "overrides.yaml")
	_, _, code := runCLI(t, configOverride, "temp", "--gpu", "", "85")
	require.Equal(t, 2, code)
	require.NoFileExists(t, configOverride, "an empty target must not be applied to any device")
}

func TestCLI_MissingTargetIsUsageError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, code := runCLI(t, filepath.Join(dir, "overrides.yaml"), "temp", "85")
	require.Equal(t, 2, code, "a mutation without --gpu must not be applied")
}

func TestCLI_UnknownCommandIsUsageError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, errStr, code := runCLI(t, filepath.Join(dir, "overrides.yaml"), "bogus")
	require.Equal(t, 2, code)
	require.Contains(t, errStr, `unknown command "bogus"`)
}

func TestCLI_HelpListsEveryCommand(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run([]string{"--help"}, &out, &errb)
	require.Equalf(t, 0, code, "--help exited %d: %s", code, errb.String())
	for _, want := range []string{"fail", "temp", "sram-ecc", "fabric-health", "mig", "status", "reset", "watch-allocations"} {
		require.Containsf(t, out.String(), want, "help output missing %q", want)
	}
}

func TestCLI_ResetGPU(t *testing.T) {
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	_, e, c := runCLI(t, configOverride, "fail", "--gpu", "1", "--mode", "lost")
	require.Equalf(t, 0, c, "setup fail: %s", e)
	_, e, c = runCLI(t, configOverride, "reset", "--gpu", "1")
	require.Equalf(t, 0, c, "reset: %s", e)
	require.NotContains(t, readConfigOverride(t, configOverride), "lost", "reset did not remove device 1")
}

// migTestConfig writes a profile for a two-GPU MIG-capable node, since the
// layout check and the in-use guard both need a base config to resolve
// against. GPU 1 declares a compute process, which is what the guard refuses.
func migTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `version: "1.0"
system:
  driver_version: "550.163"
  nvml_version: "12.550.163"
  num_devices: 2
device_defaults:
  name: NVIDIA A100-SXM4-40GB
  memory:
    total_bytes: 42949672960
    free_bytes: 42949672960
  mig:
    mode_current: disabled
    mode_pending: disabled
    max_gpu_instances: 7
    gpu_instances:
      - profile: 1g.5gb
        count: 7
devices:
  - index: 1
    processes:
      - pid: 4242
        used_gpu_memory: 1073741824
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// runMigCLI runs the CLI with both the override file and a real profile, which
// the mig command needs to resolve a layout.
func runMigCLI(t *testing.T, configOverride, config string, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"--file", configOverride, "--config", config}, args...)
	var out, errb bytes.Buffer
	code := run(full, &out, &errb)
	return out.String(), errb.String(), code
}

func TestCLI_MigEnableWritesLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	out, errStr, code := runMigCLI(t, configOverride, migTestConfig(t),
		"mig", "--gpu", "0", "enable", "--profile", "1g.5gb", "--count", "7")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)
	require.Contains(t, out, "ok:")

	s := readConfigOverride(t, configOverride)
	for _, want := range []string{"mode_current: enabled", "mode_pending: enabled", "profile: 1g.5gb", "count: 7"} {
		require.Containsf(t, s, want, "override missing %q", want)
	}
}

func TestCLI_MigEnableBareOmitsLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t), "mig", "--gpu", "0", "enable")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)

	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "mode_current: enabled")
	require.NotContains(t, s, "gpu_instances",
		"a bare enable must leave the profile's declared layout in charge")
}

// A disable destroys every partition, so it is guarded like an enable — which
// is why this one has to say --force to reach the node's busy device.
func TestCLI_MigDisableWritesBothModes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t), "mig", "--gpu", "all", "disable", "--force")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)

	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "mode_current: disabled")
	require.Contains(t, s, "mode_pending: disabled")
}

// TestCLI_MigDisableRefusesABusyDevice: a disable destroys every partition, so
// it strands running work at least as thoroughly as a repartition and is
// refused on the same terms.
func TestCLI_MigDisableRefusesABusyDevice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t), "mig", "--gpu", "all", "disable")
	require.Equal(t, exitFailure, code)
	require.Contains(t, errStr, "in use")
	require.NoFileExists(t, configOverride)
}

// TestCLI_MigSwapLeavesNoStaleLayout is the end-to-end form of the
// authoritative write: two enables in a row leave one layout in the file.
func TestCLI_MigSwapLeavesNoStaleLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")
	config := migTestConfig(t)

	_, _, code := runMigCLI(t, configOverride, config,
		"mig", "--gpu", "0", "enable", "--profile", "1g.5gb", "--count", "7")
	require.Equal(t, 0, code)
	_, errStr, code := runMigCLI(t, configOverride, config,
		"mig", "--gpu", "0", "enable", "--profile", "3g.20gb", "--count", "2")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)

	s := readConfigOverride(t, configOverride)
	require.Contains(t, s, "profile: 3g.20gb")
	require.NotContains(t, s, "profile: 1g.5gb", "the previous layout must not survive")
}

func TestCLI_MigCountWithoutProfileIsAUsageError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, _, code := runMigCLI(t, configOverride, migTestConfig(t),
		"mig", "--gpu", "0", "enable", "--count", "4")
	require.Equal(t, exitUsage, code)
}

// The layout check returns a plain error, which run() reports as a usage error
// — the operator asked for a layout this board cannot hold, not a node the CLI
// failed to write to.
func TestCLI_MigRejectsAProfileTheBoardDoesNotOffer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t),
		"mig", "--gpu", "0", "enable", "--profile", "1g.6gb", "--count", "7")
	require.Equal(t, exitUsage, code, "an unknown profile must fail loudly")
	require.Contains(t, errStr, "cannot hold the requested layout")
	require.NoFileExists(t, configOverride, "a rejected command must not write")
}

// TestCLI_MigRefusesABusyDevice: repartitioning under a running workload is
// what the driver itself refuses.
func TestCLI_MigRefusesABusyDevice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t),
		"mig", "--gpu", "1", "enable", "--profile", "1g.5gb", "--count", "7")
	require.Equal(t, exitFailure, code)
	require.Contains(t, errStr, "in use")
	require.NoFileExists(t, configOverride)
}

func TestCLI_MigForceOverridesTheInUseGuard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t),
		"mig", "--gpu", "1", "enable", "--profile", "1g.5gb", "--count", "7", "--force")
	require.Equalf(t, 0, code, "exit %d: %s", code, errStr)
	require.Contains(t, readConfigOverride(t, configOverride), "profile: 1g.5gb")
}

func TestCLI_MigWithoutASubcommandIsAUsageError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, errStr, code := runMigCLI(t, configOverride, migTestConfig(t), "mig", "--gpu", "0")
	require.Equal(t, exitUsage, code, "naming no verb must not exit clean")
	require.Contains(t, errStr, "enable")
	require.NoFileExists(t, configOverride)
}

// TestCLI_MigAllRefusesWhenAnyDeviceIsBusy: the shared bucket reaches every
// device, so one busy device is enough to refuse.
func TestCLI_MigAllRefusesWhenAnyDeviceIsBusy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configOverride := filepath.Join(dir, "overrides.yaml")

	_, _, code := runMigCLI(t, configOverride, migTestConfig(t),
		"mig", "--gpu", "all", "enable", "--profile", "1g.5gb", "--count", "7")
	require.Equal(t, exitFailure, code)
}
