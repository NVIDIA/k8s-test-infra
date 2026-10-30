// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package hack

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// foldChangie stands in for changie. It appends its arguments to
// $CHANGIE_CALLS and prints the section `changie batch <version> --dry-run`
// renders for one pending fragment.
const foldChangie = `#!/bin/sh
printf '%s\n' "$*" >> "$CHANGIE_CALLS"
printf '## [%s] - 2026-10-13\n\n### Fixed\n\n- A pending fix.\n' "$2"
`

// TestChangelogFold pins what hack/changelog-fold.sh does to CHANGELOG.md and
// the pending fragments. A plain X.Y.Z folds the fragments into a new section
// above the newest version and deletes them. Every refusal exits 1 before
// changie runs and leaves CHANGELOG.md and the fragments exactly as they were:
// the fragment delete is unconditional once the checks pass, so a version or
// changelog the script must not fold must never get that far.
func TestChangelogFold(t *testing.T) {
	t.Parallel()

	script := filepath.Join(repoRoot(t), "hack", "changelog-fold.sh")

	const (
		header   = "# Changelog\n\nAll notable changes to this project are documented in this file.\n\n"
		released = "## [0.4.1] - 2026-10-12\n\n### Fixed\n\n- A released fix.\n\n" +
			"[0.4.1]: https://github.com/NVIDIA/k8s-test-infra/compare/v0.4.0...v0.4.1\n"
		section    = "## [0.5.0] - 2026-10-13\n\n### Fixed\n\n- A pending fix.\n"
		changelog  = header + released
		unreleased = header + "## [Unreleased]\n\n- A hand-written entry.\n\n" + released
		existing   = header + "## [0.5.0] - 2026-10-01\n\n- Already released.\n\n" + released
		batch      = "batch 0.5.0 --dry-run\n"
	)
	// The script's [Unreleased] refusal separates its clauses with U+2014.
	dash := string(rune(0x2014))
	fragments := map[string]string{
		"Added-20261013-120000.yaml": "kind: Added\nbody: A pending feature.\ntime: 2026-10-13T12:00:00Z\n",
		"Fixed-20261013-120001.yaml": "kind: Fixed\nbody: A pending fix.\ntime: 2026-10-13T12:00:01Z\n",
	}
	notPlain := func(v string) string {
		return "'" + v + "' is not an unprefixed X.Y.Z version such as 0.5.0\n"
	}

	for _, tc := range []struct {
		name          string
		changelog     string
		args          []string
		exitCode      int
		stdout        string
		stderr        string
		wantChangelog string
		wantFragments bool
		changieCalls  string
	}{
		// Release candidates are not published, so they are not folded either.
		{
			name: "prerelease version", changelog: changelog, args: []string{"0.5.0-rc1"},
			exitCode: 1, stderr: notPlain("0.5.0-rc1"),
			wantChangelog: changelog, wantFragments: true,
		},
		// CHANGELOG.md headings carry no v prefix; the v belongs to the git tag.
		{
			name: "v-prefixed version", changelog: changelog, args: []string{"v0.5.0"},
			exitCode: 1, stderr: notPlain("v0.5.0"),
			wantChangelog: changelog, wantFragments: true,
		},
		{
			name: "unreleased heading", changelog: unreleased, args: []string{"0.5.0"},
			exitCode: 1,
			stderr: "CHANGELOG.md has an '## [Unreleased]' section " + dash +
				" move those entries into .changes/unreleased/ fragments first\n",
			wantChangelog: unreleased, wantFragments: true,
		},
		{
			name: "existing section", changelog: existing, args: []string{"0.5.0"},
			exitCode: 1, stderr: "CHANGELOG.md already has a '## [0.5.0]' section\n",
			wantChangelog: existing, wantFragments: true,
		},
		{
			name: "dry run", changelog: changelog, args: []string{"--dry-run", "0.5.0"},
			stdout:        section,
			wantChangelog: changelog, wantFragments: true, changieCalls: batch,
		},
		{
			name: "fold", changelog: changelog, args: []string{"0.5.0"},
			stdout:        "Folded 0.5.0 into CHANGELOG.md.\n",
			wantChangelog: header + section + "\n" + released, changieCalls: batch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The working directory stands in for the repository root; changie and
			// its call log live elsewhere so they cannot show up in it.
			work, tools := t.TempDir(), t.TempDir()
			pending := filepath.Join(work, ".changes", "unreleased")
			require.NoError(t, os.MkdirAll(pending, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(work, "CHANGELOG.md"), []byte(tc.changelog), 0o644))
			for name, body := range fragments {
				require.NoError(t, os.WriteFile(filepath.Join(pending, name), []byte(body), 0o644))
			}
			changie := filepath.Join(tools, "changie")
			require.NoError(t, os.WriteFile(changie, []byte(foldChangie), 0o755))
			calls := filepath.Join(tools, "calls")

			// The Makefile runs `bash hack/changelog-fold.sh` from the directory
			// that holds CHANGELOG.md, with CHANGIE naming the binary.
			var stdout, stderr bytes.Buffer
			cmd := exec.Command("bash", append([]string{script}, tc.args...)...)
			cmd.Dir = work
			cmd.Env = append(os.Environ(), "CHANGIE="+changie, "CHANGIE_CALLS="+calls)
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

			got, err := os.ReadFile(filepath.Join(work, "CHANGELOG.md"))
			require.NoError(t, err)
			require.Equal(t, tc.wantChangelog, string(got), "CHANGELOG.md")

			left := map[string]string{}
			entries, err := os.ReadDir(pending)
			require.NoError(t, err)
			for _, entry := range entries {
				body, err := os.ReadFile(filepath.Join(pending, entry.Name()))
				require.NoError(t, err)
				left[entry.Name()] = string(body)
			}
			if tc.wantFragments {
				require.Equal(t, fragments, left, "pending fragments")
			} else {
				require.Empty(t, left, "pending fragments")
			}

			loggedCalls, err := os.ReadFile(calls)
			if errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
			require.NoError(t, err)
			require.Equal(t, tc.changieCalls, string(loggedCalls), "changie calls")
		})
	}
}
