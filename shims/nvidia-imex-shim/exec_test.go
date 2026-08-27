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
	"testing"

	"github.com/stretchr/testify/require"
)

// prebuiltShim returns the path to the binary produced by `make build`.
// Tests are skipped when the binary is absent so `go test ./...` still
// passes on a clean checkout without a prior build.
func prebuiltShim(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	var root string
	for cur := wd; cur != "/"; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			root = cur
			break
		}
	}
	require.NotEmpty(t, root, "could not locate repo root")
	bin := filepath.Join(root, "dist", "nvidia-imex-shim")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("pre-built binary not found at %s — run 'make build' first", bin)
	}
	return bin
}

// writeStub copies testdata/nvidia-imex.real into dir with exec permission.
func writeStub(t *testing.T, dir string) string {
	t.Helper()
	src, err := os.ReadFile("testdata/nvidia-imex.real")
	require.NoError(t, err)
	stub := filepath.Join(dir, "nvidia-imex.real")
	require.NoError(t, os.WriteFile(stub, src, 0o755))
	return stub
}

func TestShimExecsRealWithNogpu(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	shim := prebuiltShim(t)
	stub := writeStub(t, tmp)

	cmd := exec.Command(shim, "-c", "/imexd/imexd.cfg")
	cmd.Env = append(os.Environ(), envRealBin+"="+stub, "ENV_PROBE=carried-through")
	out, err := cmd.Output()

	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee, "stub exits 7; shim must surface the real binary's exit code")
	require.Equal(t, 7, ee.ExitCode(), "exit code must pass through exec")
	require.Equal(t, "-c\n/imexd/imexd.cfg\n--nogpu\nENV_PROBE=carried-through\n", string(out))
}

func TestShimMissingRealBinary(t *testing.T) {
	t.Parallel()
	shim := prebuiltShim(t)

	cmd := exec.Command(shim, "-c", "/cfg")
	cmd.Env = append(os.Environ(), envRealBin+"=/nonexistent/nvidia-imex.real")
	out, err := cmd.CombinedOutput()

	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, 127, ee.ExitCode(), "conventional command-not-found code")
	require.Contains(t, string(out), "nvidia-imex-shim: exec /nonexistent/nvidia-imex.real")
}
