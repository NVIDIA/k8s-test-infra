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

// Package makefile guards build-system invariants that no Go unit test covers.
//
// The `e2e` target pipes ginkgo through `tee`, so the recipe's exit status is
// tee's unless the shell runs with `pipefail`. The Makefile sets `.SHELLFLAGS
// := -o pipefail -ec`, but GNU Make only added `.SHELLFLAGS` in 3.82. macOS
// ships 3.81, which parses the line as an ordinary variable and invokes the
// shell with a bare `-c`. On such a host a red suite exited 0, and `make e2e &&
// git push` pushed on a failing suite (issue #560).
//
// These tests therefore assert the FAILING direction: a red suite must produce
// a non-zero status. The `without_shellflags` case neutralizes `.SHELLFLAGS`
// from the command line so the guard reproduces the 3.81 behaviour on any make
// version, including the 4.x used by CI.
package makefile

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRoot resolves the repository root from this source file's own location.
// Deriving it from the working directory or from $HOME would let the test
// green-light a different checkout than the one it ships beside.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller could not locate this test file")

	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	require.FileExists(t, filepath.Join(root, "Makefile"),
		"repo root %q does not hold a Makefile", root)

	return root
}

// writeGinkgoStub writes an executable that mimics a ginkgo run: it prints the
// suite banner and exits with exitCode. It replaces $(GINKGO) so the target
// runs without a Kind cluster.
func writeGinkgoStub(t *testing.T, dir string, exitCode int) string {
	t.Helper()

	body := "#!/bin/sh\n" +
		"echo 'Ran 16 of 63 Specs in 263.999 seconds'\n"
	if exitCode == 0 {
		body += "echo 'SUCCESS! -- 16 Passed | 0 Failed | 0 Pending | 47 Skipped'\n"
	} else {
		body += "echo 'FAIL! -- 15 Passed | 1 Failed | 0 Pending | 47 Skipped'\n"
	}
	body += "exit " + strconv.Itoa(exitCode) + "\n"

	path := filepath.Join(dir, "ginkgo-stub")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))

	return path
}

// runE2E runs the real `e2e` target against a stub ginkgo and reports the exit
// code make returned. It runs make in a scratch directory via -C so the
// recipe's `tee e2e.log` cannot clobber the developer's own e2e.log.
func runE2E(t *testing.T, exitCode int, extraArgs ...string) (int, string) {
	t.Helper()

	makeBin, err := exec.LookPath("make")
	require.NoError(t, err, "make is required to build this repository")

	work := t.TempDir()
	stub := writeGinkgoStub(t, work, exitCode)

	args := append([]string{
		"-C", work,
		"-f", filepath.Join(repoRoot(t), "Makefile"),
		"e2e",
		"GINKGO=" + stub,
	}, extraArgs...)

	out, err := exec.Command(makeBin, args...).CombinedOutput()
	if err == nil {
		return 0, string(out)
	}

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "make failed to run: %v", err)

	return exitErr.ExitCode(), string(out)
}

// TestE2EPropagatesSuiteFailure is the regression guard for issue #560.
func TestE2EPropagatesSuiteFailure(t *testing.T) {
	t.Parallel()

	t.Run("red suite fails with the Makefile's own shell flags", func(t *testing.T) {
		t.Parallel()

		code, out := runE2E(t, 1)
		require.NotZero(t, code,
			"`make e2e` reported success for a failing suite; a red suite must not exit 0.\n%s", out)
	})

	// The load-bearing case. Neutralizing .SHELLFLAGS reproduces GNU Make 3.81,
	// which ignores it, and proves the recipe carries its own pipefail rather
	// than depending on a global set 150 lines away.
	t.Run("red suite fails without .SHELLFLAGS", func(t *testing.T) {
		t.Parallel()

		code, out := runE2E(t, 1, ".SHELLFLAGS=-c")
		require.NotZero(t, code,
			"`make e2e` reported success for a failing suite when .SHELLFLAGS was ignored, "+
				"which is what GNU Make 3.81 (stock on macOS) always does.\n%s", out)
	})

	// Guards against a degenerate fix that makes the target always fail.
	t.Run("green suite still succeeds", func(t *testing.T) {
		t.Parallel()

		code, out := runE2E(t, 0, ".SHELLFLAGS=-c")
		require.Zero(t, code, "`make e2e` failed for a passing suite.\n%s", out)
	})
}
