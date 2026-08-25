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

// Package tilt guards invariants of the Tiltfiles under local/.
//
// Tilt's docker_build(only=[...]) restricts the build context. Omitting a path
// the Dockerfile COPYs does not surface as a missing file at that COPY: BuildKit
// fails the whole build with "failed to compute cache key: ... not found",
// naming a content hash rather than the flag that excluded the path.
//
// Two Tiltfiles build deployments/nvml-mock/Dockerfile — local/nvml_mock.tiltfile
// for the default path, local/compute-domain/compute_domain.tiltfile for the
// ComputeDomain scenario. They once held separate copies of the list, so adding
// internal/ and Makefile to the Dockerfile while updating only the first copy
// silently broke every `tilt up -- --compute-domain` (NVIDIA/k8s-test-infra#497).
package tilt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockDockerfile is the Dockerfile whose build context these assertions govern,
// spelled as the Tiltfiles spell it (repo-relative, forward slashes).
const mockDockerfile = "deployments/nvml-mock/Dockerfile"

// tiltfiles are scanned both for builds of mockDockerfile and for the constants
// those builds reference; MOCK_IMAGE_BUILD_INPUTS is declared in the first and
// load()ed by the second.
var tiltfiles = []string{
	"local/nvml_mock.tiltfile",
	"local/compute-domain/compute_domain.tiltfile",
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller could not locate this test file")

	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	require.FileExists(t, filepath.Join(root, mockDockerfile),
		"repo root %q does not hold %s", root, mockDockerfile)

	return root
}

// TestBuildInputsCoverDockerfileCopies asserts that every Tilt build of the
// nvml-mock image admits each path that image's Dockerfile COPYs from the build
// context. It fails for a build that omits one, and for a build whose only=
// cannot be resolved at all — an unrestricted-looking build is far more likely
// to be a parse miss here than a deliberate choice.
func TestBuildInputsCoverDockerfileCopies(t *testing.T) {
	root := repoRoot(t)

	dockerfile, err := os.ReadFile(filepath.Join(root, mockDockerfile))
	require.NoError(t, err)

	copied := ContextCopySources(string(dockerfile))
	require.NotEmpty(t, copied, "parsed no context COPY sources out of %s", mockDockerfile)

	sources := map[string]string{}
	strs, lists := map[string]string{}, map[string][]string{}
	for _, rel := range tiltfiles {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		sources[rel] = string(raw)

		fileStrs, fileLists := Constants(string(raw))
		for k, v := range fileStrs {
			strs[k] = v
		}
		for k, v := range fileLists {
			lists[k] = v
		}
	}

	checked := 0
	for _, rel := range tiltfiles {
		for _, build := range DockerBuilds(sources[rel], strs, lists) {
			if build.Dockerfile != mockDockerfile {
				continue
			}
			checked++

			require.NotEmpty(t, build.Only,
				"%s: build of %s declares no resolvable only= list", rel, mockDockerfile)

			for _, path := range copied {
				require.True(t, admits(build.Only, path),
					"%s: build context excludes %q, which %s COPYs — add it to the only= list",
					rel, path, mockDockerfile)
			}
		}
	}

	// Guard the guard: a moved or renamed docker_build call would otherwise let
	// this test pass without inspecting anything.
	require.Equal(t, len(tiltfiles), checked,
		"expected one build of %s per scanned Tiltfile", mockDockerfile)
}

// admits reports whether a build-context restriction allows path, i.e. path
// equals an entry or sits under a directory entry.
func admits(only []string, path string) bool {
	for _, entry := range only {
		if path == entry {
			return true
		}
		if strings.HasSuffix(entry, "/") && strings.HasPrefix(path, entry) {
			return true
		}
	}
	return false
}
