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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildShim(t *testing.T, out string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("unsupported GOOS=%s for build/exec integration", runtime.GOOS)
	}
	cmd := exec.Command("go", "build", "-mod=vendor", "-o", out, "./cmd/imex-nogpu-shim")
	cmd.Dir = shimRepoRoot(t)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "build shim: %s", output)
	return out
}

func shimRepoRoot(t *testing.T) string {
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

// writeStub creates a fake "real" nvidia-imex that prints its argv and
// an env probe, then exits 7 — so the test can independently verify
// argv construction, environment passthrough, and exit-code
// transparency of the exec.
func writeStub(t *testing.T, dir string) string {
	t.Helper()
	stub := filepath.Join(dir, "nvidia-imex.real")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\nprintf 'ENV_PROBE=%s\\n' \"$ENV_PROBE\"\nexit 7\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o755))
	return stub
}

func TestShimExecsRealWithNogpu(t *testing.T) {
	tmp := t.TempDir()
	shim := buildShim(t, filepath.Join(tmp, "shim"))
	stub := writeStub(t, tmp)

	cmd := exec.Command(shim, "-c", "/imexd/imexd.cfg")
	cmd.Env = append(os.Environ(), envRealBin+"="+stub, "ENV_PROBE=carried-through")
	out, err := cmd.Output()

	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee, "stub exits 7; shim must surface the real binary's exit code")
	assert.Equal(t, 7, ee.ExitCode(), "exit code must pass through exec")
	assert.Equal(t, "-c\n/imexd/imexd.cfg\n--nogpu\nENV_PROBE=carried-through\n", string(out))
}

func TestShimMissingRealBinary(t *testing.T) {
	tmp := t.TempDir()
	shim := buildShim(t, filepath.Join(tmp, "shim"))

	cmd := exec.Command(shim, "-c", "/cfg")
	cmd.Env = append(os.Environ(), envRealBin+"=/nonexistent/nvidia-imex.real")
	out, err := cmd.CombinedOutput()

	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 127, ee.ExitCode(), "conventional command-not-found code")
	assert.Contains(t, string(out), "imex-nogpu-shim: exec /nonexistent/nvidia-imex.real")
}
