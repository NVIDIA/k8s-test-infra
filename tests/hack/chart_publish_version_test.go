// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package hack

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChartPublishVersion pins what hack/chart-publish-version.sh returns: a
// stable release tag maps to its own version, main maps to the chart version
// only when that is a -dev version, and every other ref exits 1 with a
// refusal on stderr.
func TestChartPublishVersion(t *testing.T) {
	t.Parallel()

	script := filepath.Join(repoRoot(t), "hack", "chart-publish-version.sh")

	refusedFromMain := func(v string) string {
		return "refusing to publish chart version " + v +
			" from main: only -dev versions publish from main, and release versions publish from a stable vMAJOR.MINOR.PATCH tag\n"
	}
	refusedRef := func(ref string) string {
		return "refusing to publish from " + ref +
			": charts publish only from refs/heads/main and stable vMAJOR.MINOR.PATCH release tags\n"
	}
	usage := "usage: " + script + " <github-ref> <chart-version>\n"

	for _, tc := range []struct {
		name     string
		args     []string
		exitCode int
		stdout   string
		stderr   string
	}{
		// The tag decides the printed version, whatever Chart.yaml says. How the
		// workflow packages with that version is not covered here.
		{name: "stable tag", args: []string{"refs/tags/v0.5.0", "0.5.0-dev"}, stdout: "0.5.0\n"},
		{name: "multi-digit tag", args: []string{"refs/tags/v12.345.6789", "0.1.0"}, stdout: "12.345.6789\n"},
		{name: "zero tag", args: []string{"refs/tags/v0.0.0", "0.5.0-dev"}, stdout: "0.0.0\n"},

		// main publishes the chart's own -dev version unchanged.
		{name: "main dev", args: []string{"refs/heads/main", "0.5.0-dev"}, stdout: "0.5.0-dev\n"},
		{
			name: "main release version", args: []string{"refs/heads/main", "0.5.0"},
			exitCode: 1, stderr: refusedFromMain("0.5.0"),
		},
		{
			name: "main prerelease version", args: []string{"refs/heads/main", "0.5.0-rc1"},
			exitCode: 1, stderr: refusedFromMain("0.5.0-rc1"),
		},
		{
			name: "main dev not a suffix", args: []string{"refs/heads/main", "0.5.0-dev.1"},
			exitCode: 1, stderr: refusedFromMain("0.5.0-dev.1"),
		},
		{
			name: "main empty version", args: []string{"refs/heads/main", ""},
			exitCode: 1, stderr: refusedFromMain(""),
		},

		// Every other ref is refused, whatever the chart version.
		{
			name: "feature branch", args: []string{"refs/heads/feature/x", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/heads/feature/x"),
		},
		{
			name: "main-like branch", args: []string{"refs/heads/main-fix", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/heads/main-fix"),
		},
		{
			name: "partial version tag", args: []string{"refs/tags/v1.2", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2"),
		},
		{
			name: "build metadata tag", args: []string{"refs/tags/v1.2.3+b1", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2.3+b1"),
		},
		{
			name: "leading zero major tag", args: []string{"refs/tags/v01.2.3", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v01.2.3"),
		},
		{
			name: "leading zero minor tag", args: []string{"refs/tags/v0.05.0", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v0.05.0"),
		},
		{
			name: "leading zero patch tag", args: []string{"refs/tags/v1.2.03", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2.03"),
		},

		// Release candidates are not published: only stable tags release.
		{
			name: "prerelease tag", args: []string{"refs/tags/v0.5.0-rc1", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v0.5.0-rc1"),
		},
		{
			name: "dotted prerelease tag", args: []string{"refs/tags/v1.2.3-rc.1", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2.3-rc.1"),
		},
		{
			name: "hyphenated prerelease tag", args: []string{"refs/tags/v1.2.3-alpha-1.0", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2.3-alpha-1.0"),
		},
		{
			name: "leading zero numeric prerelease tag", args: []string{"refs/tags/v1.2.3-01", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2.3-01"),
		},
		{
			name: "empty prerelease identifier tag", args: []string{"refs/tags/v1.2.3-rc..1", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1.2.3-rc..1"),
		},
		{
			name: "missing v prefix tag", args: []string{"refs/tags/1.2.3", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/1.2.3"),
		},
		{
			name: "tag-like branch", args: []string{"refs/heads/v1.2.3", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/heads/v1.2.3"),
		},
		{
			name: "tag ref not at the start", args: []string{"refs/tags/x/refs/tags/v1.2.3", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/x/refs/tags/v1.2.3"),
		},
		{
			name: "hyphen separated tag", args: []string{"refs/tags/v1-2-3", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v1-2-3"),
		},
		{
			name: "unseparated tag", args: []string{"refs/tags/v10203", "0.5.0-dev"},
			exitCode: 1, stderr: refusedRef("refs/tags/v10203"),
		},

		{name: "no arguments", args: []string{}, exitCode: 2, stderr: usage},
		{name: "one argument", args: []string{"refs/heads/main"}, exitCode: 2, stderr: usage},
		{
			name: "three arguments", args: []string{"refs/heads/main", "0.5.0-dev", "extra"},
			exitCode: 2, stderr: usage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The workflows run ./hack/chart-publish-version.sh, so run it the
			// same way: through its shebang, which needs the executable bit.
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(script, tc.args...)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			exitCode := 0
			if err := cmd.Run(); err != nil {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr, "running %s", script)
				exitCode = exitErr.ExitCode()
			}

			require.Equal(t, tc.exitCode, exitCode, "exit code; stderr: %s", stderr.String())
			require.Equal(t, tc.stdout, stdout.String(), "stdout")
			require.Equal(t, tc.stderr, stderr.String(), "stderr")
		})
	}
}
