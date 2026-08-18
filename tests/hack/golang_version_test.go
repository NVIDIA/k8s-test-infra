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

// Package hack guards invariants of the helper scripts under hack/.
//
// hack/golang-version.sh is the single source of truth for the Go toolchain
// version: .github/workflows/variables.yaml writes its output straight into
// $GITHUB_OUTPUT, and docs/demo/compute-domain/run.sh passes it as a Docker
// --build-arg. Both consumers require exactly one whitespace-free token.
//
// The original implementation extracted the version with `grep -oE "[0-9\.]+"`,
// which matches *every* run of digits and dots on the line. On a release tag
// like `golang:1.26.6` that happens to be one match, but on a prerelease tag
// like `golang:1.27rc1` it is two — "1.27" and "1" — and the unquoted `echo`
// collapses them into the single string "1.27 1". Feeding that into
// $GITHUB_OUTPUT breaks the Go-version fan-out for every downstream workflow.
//
// These tests therefore assert the EXACT parsed token for each tag shape, so a
// regression that reintroduces multi-match parsing fails here rather than in CI
// on the day someone bumps to a release candidate.
package hack

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	require.FileExists(t, filepath.Join(root, "hack", "golang-version.sh"),
		"repo root %q does not hold hack/golang-version.sh", root)

	return root
}

// runAgainstTag stages the real script beside a synthetic Dockerfile carrying
// the given `FROM golang:<tag>` line, in the directory layout the script
// expects (it locates the Dockerfile relative to its own path). The script
// under test is copied from the repo, never rewritten, so the test exercises
// the shipped implementation rather than a paraphrase of it.
func runAgainstTag(t *testing.T, tag string) string {
	t.Helper()

	root := repoRoot(t)
	stage := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(stage, "hack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(stage, "deployments", "devel"), 0o755))

	script, err := os.ReadFile(filepath.Join(root, "hack", "golang-version.sh"))
	require.NoError(t, err)
	stagedScript := filepath.Join(stage, "hack", "golang-version.sh")
	require.NoError(t, os.WriteFile(stagedScript, script, 0o755))

	dockerfile := "FROM golang:" + tag + "\nRUN echo build\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(stage, "deployments", "devel", "Dockerfile"),
		[]byte(dockerfile), 0o644))

	out, err := exec.Command("bash", stagedScript).Output()
	require.NoError(t, err, "script exited non-zero for tag %q", tag)

	return string(out)
}

// TestGolangVersionParsesTag pins the exact output for each tag shape the
// golang image publishes. The prerelease cases are the regression guard: they
// fail with the multi-match implementation and pass with a single-token one.
func TestGolangVersionParsesTag(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  string
		want string
	}{
		{name: "patch release", tag: "1.26.6", want: "1.26.6"},
		{name: "release candidate", tag: "1.27rc1", want: "1.27rc1"},
		{name: "beta", tag: "1.28beta2", want: "1.28beta2"},
		{name: "minor only", tag: "1.27", want: "1.27"},
		{name: "release with variant", tag: "1.26.6-bookworm", want: "1.26.6"},
		{name: "prerelease with variant", tag: "1.27rc1-alpine", want: "1.27rc1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runAgainstTag(t, tc.tag)
			require.Equal(t, tc.want+"\n", got,
				"golang-version.sh must emit exactly %q for tag %q", tc.want, tc.tag)
		})
	}
}

// TestGolangVersionEmitsSingleToken asserts the property the consumers actually
// depend on, independently of any specific tag: one line, no embedded
// whitespace. $GITHUB_OUTPUT rejects a bare multiline value, and a
// space-separated one silently becomes a wrong --build-arg.
func TestGolangVersionEmitsSingleToken(t *testing.T) {
	for _, tag := range []string{"1.26.6", "1.27rc1", "1.28beta2", "1.26.6-bookworm"} {
		t.Run(tag, func(t *testing.T) {
			got := runAgainstTag(t, tag)

			require.NotEmpty(t, got, "script produced no output for %q", tag)

			lines := splitNonEmptyLines(got)
			require.Len(t, lines, 1,
				"expected exactly one output line for %q, got %q", tag, got)
			require.NotContains(t, lines[0], " ",
				"output for %q must not contain a space, got %q", tag, got)
		})
	}
}

// TestGolangVersionMatchesCheckedInDockerfile runs the script as shipped,
// against the repository's real Dockerfile, and asserts the result is a single
// usable token. This catches a Dockerfile edit that the synthetic cases above
// would not see.
func TestGolangVersionMatchesCheckedInDockerfile(t *testing.T) {
	root := repoRoot(t)

	out, err := exec.Command("bash", filepath.Join(root, "hack", "golang-version.sh")).Output()
	require.NoError(t, err)

	lines := splitNonEmptyLines(string(out))
	require.Len(t, lines, 1, "expected a single version line, got %q", string(out))
	require.NotContains(t, lines[0], " ", "version token must not contain a space, got %q", lines[0])

	dockerfile, err := os.ReadFile(filepath.Join(root, "deployments", "devel", "Dockerfile"))
	require.NoError(t, err)
	require.Contains(t, string(dockerfile), "FROM golang:"+lines[0],
		"parsed version %q does not appear in the Dockerfile's FROM line", lines[0])
}

// splitNonEmptyLines returns the non-blank lines of s, trimming the trailing
// newline that a shell `echo` always appends.
func splitNonEmptyLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			if line := s[start:i]; line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	return out
}
