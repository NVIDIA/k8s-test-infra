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

package fakeimex_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// build compiles a fake-imex sub-command into the given output path and
// returns the binary path. Skips the test on non-linux/darwin or when
// `go` is unavailable (e.g. in stripped CI sandboxes).
func build(t *testing.T, pkg, out string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("unsupported GOOS=%s for build/exec integration", runtime.GOOS)
	}
	cmd := exec.Command("go", "build", "-mod=vendor", "-o", out, pkg)
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "build %s: %s", pkg, output)
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for cur := wd; cur != "/"; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
	}
	require.FailNow(t, "could not locate repo root")
	return ""
}

// TestFakeImex_HappyPath_ReadyTransitions starts three fake daemons and
// verifies the fake ctl reports READY only once every peer's marker has
// landed, then NOT READY after one peer goes away.
func TestFakeImex_HappyPath_ReadyTransitions(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	nodesCfg := filepath.Join(tmp, "nodes.cfg")
	require.NoError(t, os.WriteFile(nodesCfg, []byte("10.0.0.1\n10.0.0.2\n10.0.0.3\n"), 0o644))

	daemonBin := build(t, "./cmd/fake-imex/daemon", filepath.Join(tmp, "nvidia-imex"))
	ctlBin := build(t, "./cmd/fake-imex/ctl", filepath.Join(tmp, "nvidia-imex-ctl"))

	env := []string{
		"IMEX_STATE_DIR=" + stateDir,
		"IMEX_NODES_CONFIG=" + nodesCfg,
	}

	startDaemon := func(ip string) *exec.Cmd {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, daemonBin, "-c", "/dev/null")
		cmd.Env = append(append([]string{}, env...), "POD_IP="+ip)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			cancel()
			require.NoError(t, err, "start daemon %s", ip)
		}
		t.Cleanup(func() {
			cancel()
			_ = cmd.Wait()
		})
		return cmd
	}

	runCtl := func() (stdout string, exitCode int) {
		cmd := exec.Command(ctlBin, "-c", "/dev/null", "-q")
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return string(out), ee.ExitCode()
			}
			require.NoError(t, err, "ctl unexpected error")
		}
		return string(out), 0
	}

	d1 := startDaemon("10.0.0.1")
	d2 := startDaemon("10.0.0.2")
	_ = d2
	// Only 2 of 3 markers exist → ctl exits 1.
	waitForMarker(t, stateDir, "10.0.0.1")
	waitForMarker(t, stateDir, "10.0.0.2")
	_, code := runCtl()
	require.Equal(t, 1, code, "ctl with 2/3 markers: want exit 1")

	startDaemon("10.0.0.3")
	waitForMarker(t, stateDir, "10.0.0.3")
	out, code := runCtl()
	require.Equal(t, 0, code, "ctl with 3/3: want exit 0, got %q", out)
	require.True(t, strings.HasPrefix(out, "READY"), "ctl with 3/3: want READY prefix, got %q", out)

	// Kill d1 cleanly so it removes its marker; ctl should fail again.
	_ = d1.Process.Signal(syscall.SIGTERM)
	waitForNoMarker(t, stateDir, "10.0.0.1")
	_, code = runCtl()
	require.Equal(t, 1, code, "ctl after d1 SIGTERM: want exit 1")
}

func waitForMarker(t *testing.T, dir, ip string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, ip)); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNowf(t, "marker did not appear", "marker %s did not appear within deadline", ip)
}

// TestFakeImex_DaemonReassertsMarker verifies the 2s tick re-creates
// the marker after an external delete — hostPath GC, accidental
// cleanup, ConfigMap remount. Without re-assertion the daemon would
// stay non-coordinable until restart even though it's still running.
func TestFakeImex_DaemonReassertsMarker(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	nodesCfg := filepath.Join(tmp, "nodes.cfg")
	require.NoError(t, os.WriteFile(nodesCfg, []byte("10.0.0.99\n"), 0o644))

	daemonBin := build(t, "./cmd/fake-imex/daemon", filepath.Join(tmp, "nvidia-imex"))

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, daemonBin, "-c", "/dev/null")
	cmd.Env = []string{
		"IMEX_STATE_DIR=" + stateDir,
		"IMEX_NODES_CONFIG=" + nodesCfg,
		"POD_IP=10.0.0.99",
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		require.NoError(t, err, "start daemon")
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	// Marker arrives at startup.
	waitForMarker(t, stateDir, "10.0.0.99")

	// Wipe it externally.
	require.NoError(t, os.Remove(filepath.Join(stateDir, "10.0.0.99")), "remove marker")
	waitForNoMarker(t, stateDir, "10.0.0.99")

	// The daemon's 2s tick must re-create the marker. waitForMarker has
	// a 5s deadline which absorbs the tick interval plus scheduler
	// jitter.
	waitForMarker(t, stateDir, "10.0.0.99")
}

func waitForNoMarker(t *testing.T, dir, ip string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, ip)); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNowf(t, "marker did not disappear", "marker %s did not disappear within deadline", ip)
}

// TestFakeImex_ProbeContractAndDeprecation guards two contracts.
// (1) The ctl success path prints EXACTLY "READY\n" on stdout and
// NOTHING on stderr: the upstream compute-domain-daemon compares
// CombinedOutput() to that exact string, so a deprecation banner on
// the success path would break the real daemon while the fakes are in
// their deprecation release. (2) The daemon startup log and the ctl
// failure paths announce the deprecation and its reason (replaced by
// the real nvidia-imex in NO GPU mode).
func TestFakeImex_ProbeContractAndDeprecation(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	nodesCfg := filepath.Join(tmp, "nodes.cfg")
	require.NoError(t, os.WriteFile(nodesCfg, []byte("10.0.0.1\n"), 0o644))

	daemonBin := build(t, "./cmd/fake-imex/daemon", filepath.Join(tmp, "nvidia-imex"))
	ctlBin := build(t, "./cmd/fake-imex/ctl", filepath.Join(tmp, "nvidia-imex-ctl"))

	env := []string{
		"IMEX_STATE_DIR=" + stateDir,
		"IMEX_NODES_CONFIG=" + nodesCfg,
	}

	// Failure path (no marker yet): deprecation notice on stderr.
	var stdout, stderr bytes.Buffer
	ctl := exec.Command(ctlBin, "-c", "/dev/null", "-q")
	ctl.Env = env
	ctl.Stdout, ctl.Stderr = &stdout, &stderr
	err := ctl.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, 1, ee.ExitCode())
	require.Contains(t, stderr.String(), "DEPRECATED", "ctl failure path must announce deprecation")
	require.Contains(t, stderr.String(), "--nogpu", "notice must state the replacement")

	// Usage-error path (no -q): exit 2 + deprecation notice.
	stdout.Reset()
	stderr.Reset()
	ctl = exec.Command(ctlBin, "-c", "/dev/null")
	ctl.Env = env
	ctl.Stdout, ctl.Stderr = &stdout, &stderr
	err = ctl.Run()
	require.ErrorAs(t, err, &ee)
	require.Equal(t, 2, ee.ExitCode())
	require.Contains(t, stderr.String(), "DEPRECATED", "usage-error path must announce deprecation")

	// nodes.cfg read-error path: exit 1 + deprecation notice.
	stdout.Reset()
	stderr.Reset()
	ctl = exec.Command(ctlBin, "-c", "/dev/null", "-q")
	ctl.Env = append(append([]string{}, env...), "IMEX_NODES_CONFIG="+filepath.Join(tmp, "missing-nodes.cfg"))
	ctl.Stdout, ctl.Stderr = &stdout, &stderr
	err = ctl.Run()
	require.ErrorAs(t, err, &ee)
	require.Equal(t, 1, ee.ExitCode())
	require.Contains(t, stderr.String(), "DEPRECATED", "read-error path must announce deprecation")

	// Daemon startup log (written to a file to avoid racing the pipe
	// copier; the notice is logged before WriteMarker, so the marker's
	// existence implies the notice is on disk).
	logPath := filepath.Join(tmp, "daemon.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	daemon := exec.CommandContext(ctx, daemonBin, "-c", "/dev/null")
	daemon.Env = append(append([]string{}, env...), "POD_IP=10.0.0.1")
	daemon.Stderr = logFile
	daemon.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, daemon.Start())
	t.Cleanup(func() {
		cancel()
		_ = daemon.Wait()
		_ = logFile.Close()
	})
	waitForMarker(t, stateDir, "10.0.0.1")
	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(logged), "DEPRECATED", "daemon startup must announce deprecation")
	require.Contains(t, string(logged), "--nogpu", "notice must state the replacement")

	// Success path: EXACT stdout, EMPTY stderr.
	stdout.Reset()
	stderr.Reset()
	ctl = exec.Command(ctlBin, "-c", "/dev/null", "-q")
	ctl.Env = env
	ctl.Stdout, ctl.Stderr = &stdout, &stderr
	require.NoError(t, ctl.Run())
	require.Equal(t, "READY\n", stdout.String(), "upstream compares CombinedOutput to exactly READY\\n")
	require.Empty(t, stderr.String(), "success path must stay silent or the upstream probe contract breaks")
}
