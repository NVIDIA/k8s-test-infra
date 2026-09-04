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

package hack

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMokkaControlPlanePublishRef(t *testing.T) {
	script := filepath.Join(repoRoot(t), "hack", "validate-mokka-control-plane-publish-ref.sh")

	for _, tc := range []struct {
		name    string
		ref     string
		allowed bool
	}{
		{name: "main", ref: "refs/heads/main", allowed: true},
		{name: "zero version", ref: "refs/tags/v0.0.0", allowed: true},
		{name: "stable version", ref: "refs/tags/v1.2.3", allowed: true},
		{name: "multi-digit version", ref: "refs/tags/v12.345.6789", allowed: true},
		{name: "feature branch", ref: "refs/heads/feature/publish", allowed: false},
		{name: "main-like branch", ref: "refs/heads/main-fix", allowed: false},
		{name: "partial version", ref: "refs/tags/v1.2", allowed: false},
		{name: "prerelease", ref: "refs/tags/v1.2.3-rc.1", allowed: false},
		{name: "build metadata", ref: "refs/tags/v1.2.3+build.1", allowed: false},
		{name: "leading zero major", ref: "refs/tags/v01.2.3", allowed: false},
		{name: "leading zero minor", ref: "refs/tags/v1.02.3", allowed: false},
		{name: "leading zero patch", ref: "refs/tags/v1.2.03", allowed: false},
		{name: "missing v prefix", ref: "refs/tags/1.2.3", allowed: false},
		{name: "tag-like branch", ref: "refs/heads/v1.2.3", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", script, tc.ref)
			err := cmd.Run()
			if tc.allowed {
				require.NoError(t, err, "expected ref %q to be accepted", tc.ref)
				return
			}
			require.Error(t, err, "expected ref %q to be rejected", tc.ref)
		})
	}
}
