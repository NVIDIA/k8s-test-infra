package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLintDeploySchedule covers four input variants of deploy.yaml content
// against hack/lint_deploy_schedule.sh:
//   - no schedule: trigger          → exit 0 (pass)
//   - unmarked schedule:            → exit 1 (fail)
//   - schedule: with ADR marker     → exit 0 (pass)
//   - file missing                  → exit 2 (file not found)
//
// Each subtest writes a fixture into t.TempDir() and invokes the script.
//
// This guardrail protects the cost-model assumption that downstream
// TestResult production depends on; see spec Q7.
//
// Mutation check: removing the unmarked-schedule branch causes the
// "unmarked schedule" subtest to fail; removing the marker check causes
// "marker honored" to fail.
func TestLintDeploySchedule(t *testing.T) {
	t.Parallel()

	scriptPath, err := filepath.Abs("hack/lint_deploy_schedule.sh")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("script missing at %s: %v", scriptPath, err)
	}

	cases := []struct {
		name     string
		content  string // empty content means "do not write the file"
		wantExit int
	}{
		{
			name: "no schedule",
			content: `name: Deploy
on:
  push:
    branches: [main]
jobs:
  build:
    runs-on: ubuntu-latest
`,
			wantExit: 0,
		},
		{
			name: "unmarked schedule",
			content: `name: Deploy
on:
  schedule:
    - cron: '0 * * * *'
jobs:
  build:
    runs-on: ubuntu-latest
`,
			wantExit: 1,
		},
		{
			name: "marker honored",
			content: `name: Deploy
on:
  # ADR-allowed-schedule: docs/plans/some-future-adr.md
  schedule:
    - cron: '0 * * * *'
jobs:
  build:
    runs-on: ubuntu-latest
`,
			wantExit: 0,
		},
		{
			name:     "file missing",
			content:  "",
			wantExit: 2,
		},
		{
			name: "bypass-attempt: marker as YAML value",
			content: `name: Deploy
on:
  myKey: ADR-allowed-schedule: bypass
  schedule:
    - cron: '0 * * * *'
jobs:
  build:
    runs-on: ubuntu-latest
`,
			wantExit: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			fixturePath := filepath.Join(tmp, "deploy.yaml")
			if tc.content != "" {
				if err := os.WriteFile(fixturePath, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}
			cmd := exec.Command(scriptPath, fixturePath)
			out, err := cmd.CombinedOutput()
			gotExit := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					gotExit = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected exec error: %v\noutput:\n%s", err, out)
				}
			}
			if gotExit != tc.wantExit {
				t.Fatalf("exit = %d; want %d\noutput:\n%s", gotExit, tc.wantExit, out)
			}
			// Sanity-check the output mentions the fixture for the failure case
			// so the workflow's annotation surface is exercised.
			if tc.wantExit == 1 && !strings.Contains(string(out), "ADR-allowed-schedule") {
				t.Fatalf("expected output to mention 'ADR-allowed-schedule'; got:\n%s", out)
			}
		})
	}
}
