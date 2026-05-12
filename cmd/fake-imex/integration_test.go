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
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for cur := wd; cur != "/"; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
	}
	t.Fatal("could not locate repo root")
	return ""
}

// TestFakeImex_HappyPath_ReadyTransitions starts three fake daemons and
// verifies the fake ctl reports READY only once every peer's marker has
// landed, then NOT READY after one peer goes away.
func TestFakeImex_HappyPath_ReadyTransitions(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nodesCfg := filepath.Join(tmp, "nodes.cfg")
	if err := os.WriteFile(nodesCfg, []byte("10.0.0.1\n10.0.0.2\n10.0.0.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
			t.Fatalf("start daemon %s: %v", ip, err)
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
			t.Fatalf("ctl unexpected error: %v", err)
		}
		return string(out), 0
	}

	d1 := startDaemon("10.0.0.1")
	d2 := startDaemon("10.0.0.2")
	_ = d2
	// Only 2 of 3 markers exist → ctl exits 1.
	waitForMarker(t, stateDir, "10.0.0.1")
	waitForMarker(t, stateDir, "10.0.0.2")
	if _, code := runCtl(); code != 1 {
		t.Fatalf("ctl with 2/3 markers: want exit 1, got %d", code)
	}

	startDaemon("10.0.0.3")
	waitForMarker(t, stateDir, "10.0.0.3")
	out, code := runCtl()
	if code != 0 || !strings.HasPrefix(out, "READY") {
		t.Fatalf("ctl with 3/3: want READY exit 0, got %q exit %d", out, code)
	}

	// Kill d1 cleanly so it removes its marker; ctl should fail again.
	_ = d1.Process.Signal(syscall.SIGTERM)
	waitForNoMarker(t, stateDir, "10.0.0.1")
	if _, code := runCtl(); code != 1 {
		t.Fatalf("ctl after d1 SIGTERM: want exit 1, got %d", code)
	}
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
	t.Fatalf("marker %s did not appear within deadline", ip)
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
	t.Fatalf("marker %s did not disappear within deadline", ip)
}
