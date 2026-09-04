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
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

func runMake(t *testing.T, target string, environment ...string) (string, error) {
	t.Helper()

	makeBin, err := exec.LookPath("make")
	require.NoError(t, err, "make is required to build this repository")

	cmd := exec.Command(makeBin, target)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), environment...)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

func requireFlagValue(t *testing.T, args []string, flag, value string) {
	t.Helper()

	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return
		}

	}
	require.Failf(t, "flag/value pair not found", "%q followed by %q was not in %#v", flag, value, args)
}

func TestMokkaControlPlanePublishDefaultDockerfileExists(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err)

	match := regexp.MustCompile(`(?m)^MOKKA_CONTROL_PLANE_DOCKERFILE\s*\?=\s*(\S+)\s*$`).FindSubmatch(contents)
	require.Len(t, match, 2, "Makefile must declare a default MOKKA_CONTROL_PLANE_DOCKERFILE")
	require.FileExists(t, filepath.Join(root, string(match[1])),
		"default MOKKA_CONTROL_PLANE_DOCKERFILE must exist")
}

func TestMokkaControlPlanePushRejectsMissingGolangVersion(t *testing.T) {
	t.Parallel()

	image := "registry.example.test/nvidia/mokka-control-plane"
	out, err := runMake(t, "mokka-control-plane-image-push",
		"MOKKA_CONTROL_PLANE_IMAGE="+image,
		"MOKKA_CONTROL_PLANE_TAGS="+image+":test",
		"MOKKA_CONTROL_PLANE_GOLANG_VERSION=",
	)
	require.Error(t, err, "image push unexpectedly accepted an empty Go version:\n%s", out)
	require.Contains(t, out, "MOKKA_CONTROL_PLANE_GOLANG_VERSION")
}

func TestMokkaControlPlanePublishAttestsEachPlatformSBOM(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	logFile := filepath.Join(work, "calls")
	digestFile := filepath.Join(work, "image.digest")
	sbomFile := filepath.Join(work, "image.spdx.json")
	goauthFile := filepath.Join(work, "goauth")
	require.NoError(t, os.WriteFile(goauthFile, []byte("#!/bin/sh\n"), 0o600))
	golangVersion := "1.99.0"
	goProxy := "https://proxy.example.test/artifactory/api/go/virtual"
	indexDigest := "sha256:" + strings.Repeat("a", 64)
	amd64Digest := "sha256:" + strings.Repeat("b", 64)
	arm64Digest := "sha256:" + strings.Repeat("c", 64)
	image := "registry.example.test/nvidia/mokka-control-plane"
	tags := image + ":v1.2.3\n" + image + ":sha-deadbee"
	labels := "org.opencontainers.image.title=Mokka Control Plane\n" +
		"org.opencontainers.image.description=a label with spaces"

	dockerStub := filepath.Join(work, "docker")
	writeExecutable(t, dockerStub, `#!/usr/bin/env bash
set -eu
printf 'docker\0' >> "${PUBLISH_TEST_LOG}"
for arg in "$@"; do
    printf '%s\0' "${arg}" >> "${PUBLISH_TEST_LOG}"
done
if [[ "$1" == "buildx" && "$2" == "imagetools" && "$3" == "inspect" ]]; then
    printf 'linux/amd64 %s\nlinux/arm64/v8 %s\n' "${PUBLISH_TEST_AMD64_DIGEST}" "${PUBLISH_TEST_ARM64_DIGEST}"
    exit 0
fi

iidfile=
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
    if [[ "${args[i]}" == "--iidfile" ]]; then
        iidfile="${args[i + 1]}"
    fi
done
[[ -n "${iidfile}" ]]
printf '%s\n' "${PUBLISH_TEST_INDEX_DIGEST}" > "${iidfile}"
`)

	syftStub := filepath.Join(work, "syft")
	writeExecutable(t, syftStub, `#!/usr/bin/env bash
set -eu
printf 'syft\0' >> "${PUBLISH_TEST_LOG}"
output=
for arg in "$@"; do
    printf '%s\0' "${arg}" >> "${PUBLISH_TEST_LOG}"
    if [[ "${arg}" == spdx-json=* ]]; then
        output="${arg#spdx-json=}"
    fi
done
[[ -n "${output}" ]]
printf '{"spdxVersion":"SPDX-2.3","name":"%s"}\n' "$1" > "${output}"
`)

	cosignStub := filepath.Join(work, "cosign")
	writeExecutable(t, cosignStub, `#!/usr/bin/env bash
set -eu
printf 'cosign\0' >> "${PUBLISH_TEST_LOG}"
printf '%s\0' "$@" >> "${PUBLISH_TEST_LOG}"
`)

	out, err := runMake(t, "mokka-control-plane-publish",
		"PUBLISH_TEST_LOG="+logFile,
		"PUBLISH_TEST_INDEX_DIGEST="+indexDigest,
		"PUBLISH_TEST_AMD64_DIGEST="+amd64Digest,
		"PUBLISH_TEST_ARM64_DIGEST="+arm64Digest,
		"MOKKA_CONTROL_PLANE_IMAGE="+image,
		"MOKKA_CONTROL_PLANE_TAGS="+tags,
		"MOKKA_CONTROL_PLANE_LABELS="+labels,
		"MOKKA_CONTROL_PLANE_PLATFORMS=linux/amd64,linux/arm64",
		"MOKKA_CONTROL_PLANE_CACHE_FROM=type=gha",
		"MOKKA_CONTROL_PLANE_CACHE_TO=type=gha,mode=max",
		"MOKKA_CONTROL_PLANE_GOLANG_VERSION="+golangVersion,
		"MOKKA_CONTROL_PLANE_GOPROXY="+goProxy,
		"MOKKA_CONTROL_PLANE_GOAUTH_FILE="+goauthFile,
		"MOKKA_CONTROL_PLANE_DIGEST_FILE="+digestFile,
		"MOKKA_CONTROL_PLANE_SBOM_FILE="+sbomFile,
		"MOKKA_CONTROL_PLANE_DOCKER="+dockerStub,
		"MOKKA_CONTROL_PLANE_SYFT="+syftStub,
		"MOKKA_CONTROL_PLANE_COSIGN="+cosignStub,
	)
	require.NoError(t, err, "publication target failed:\n%s", out)

	require.FileExists(t, digestFile)
	digestBytes, err := os.ReadFile(digestFile)
	require.NoError(t, err)
	require.Equal(t, indexDigest+"\n", string(digestBytes))

	amd64SBOM := sbomFile + ".linux-amd64"
	arm64SBOM := sbomFile + ".linux-arm64"
	amd64SBOMBytes, err := os.ReadFile(amd64SBOM)
	require.NoError(t, err)
	require.Contains(t, string(amd64SBOMBytes), image+"@"+amd64Digest)
	arm64SBOMBytes, err := os.ReadFile(arm64SBOM)
	require.NoError(t, err)
	require.Contains(t, string(arm64SBOMBytes), image+"@"+arm64Digest)

	logBytes, err := os.ReadFile(logFile)
	require.NoError(t, err)
	calls := parseLoggedCalls(t, logBytes)
	dockerCalls := loggedCallsNamed(calls, "docker")
	require.Len(t, dockerCalls, 3)
	requireFlagValue(t, dockerCalls[0], "--tag", image+":v1.2.3")
	requireFlagValue(t, dockerCalls[0], "--tag", image+":sha-deadbee")
	requireFlagValue(t, dockerCalls[0], "--label", "org.opencontainers.image.title=Mokka Control Plane")
	requireFlagValue(t, dockerCalls[0], "--label", "org.opencontainers.image.description=a label with spaces")
	requireFlagValue(t, dockerCalls[0], "--cache-from", "type=gha")
	requireFlagValue(t, dockerCalls[0], "--cache-to", "type=gha,mode=max")
	requireFlagValue(t, dockerCalls[0], "--build-arg", "GOLANG_VERSION="+golangVersion)
	requireFlagValue(t, dockerCalls[0], "--build-arg", "GOPROXY="+goProxy)
	requireFlagValue(t, dockerCalls[0], "--secret", "id=goauth,src="+goauthFile)
	require.Equal(t, image+"@"+indexDigest, dockerCalls[1][3])
	require.Equal(t, image+"@"+indexDigest, dockerCalls[2][3])

	syftCalls := loggedCallsNamed(calls, "syft")
	require.Len(t, syftCalls, 2)
	require.Equal(t, image+"@"+amd64Digest, syftCalls[0][0])
	require.Equal(t, image+"@"+arm64Digest, syftCalls[1][0])

	cosignCalls := loggedCallsNamed(calls, "cosign")
	require.Equal(t, [][]string{
		{"sign", "--yes", image + "@" + indexDigest},
		{"attest", "--yes", "--predicate", amd64SBOM, "--type", "spdxjson", image + "@" + amd64Digest},
		{"attest", "--yes", "--predicate", arm64SBOM, "--type", "spdxjson", image + "@" + arm64Digest},
	}, cosignCalls)
}

type loggedCall struct {
	name string
	args []string
}

func parseLoggedCalls(t *testing.T, log []byte) []loggedCall {
	t.Helper()

	var calls []loggedCall
	for _, token := range strings.Split(strings.TrimSuffix(string(log), "\x00"), "\x00") {
		switch token {
		case "docker", "syft", "cosign":
			calls = append(calls, loggedCall{name: token})
		default:
			require.NotEmpty(t, calls, "argument logged before a command: %q", token)
			calls[len(calls)-1].args = append(calls[len(calls)-1].args, token)
		}
	}

	return calls
}

func loggedCallsNamed(calls []loggedCall, name string) [][]string {
	var matches [][]string
	for _, call := range calls {
		if call.name == name {
			matches = append(matches, call.args)
		}
	}

	return matches
}

func TestMokkaControlPlaneSignRejectsDigestWithExtraContent(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	digestFile := filepath.Join(work, "image.digest")
	cosignStub := filepath.Join(work, "cosign")
	require.NoError(t, os.WriteFile(digestFile,
		[]byte("sha256:"+strings.Repeat("b", 64)+"\n\n"), 0o600))
	writeExecutable(t, cosignStub, "#!/bin/sh\nexit 0\n")

	out, err := runMake(t, "mokka-control-plane-image-sign",
		"MOKKA_CONTROL_PLANE_IMAGE=registry.example.test/nvidia/mokka-control-plane",
		"MOKKA_CONTROL_PLANE_DIGEST_FILE="+digestFile,
		"MOKKA_CONTROL_PLANE_COSIGN="+cosignStub,
	)
	require.Error(t, err, "signing unexpectedly accepted a digest file with extra content:\n%s", out)
	require.Contains(t, out, "must contain exactly one immutable sha256 digest")
}
