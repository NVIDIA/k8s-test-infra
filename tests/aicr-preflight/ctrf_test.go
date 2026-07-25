// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape taken verbatim from a real AICR CTRF report
// (NVIDIA/aicr pkg/corroborate/testdata/.../ctrf/deployment.json).
const deploymentCTRF = `{
  "reportFormat": "CTRF",
  "specVersion": "0.0.1",
  "timestamp": "2026-07-25T09:00:00Z",
  "generatedBy": "aicr validate",
  "results": {
    "tool": {"name": "aicr", "version": "v1.0.0"},
    "summary": {"tests": 2, "passed": 1, "failed": 1, "skipped": 0, "pending": 0, "other": 0},
    "tests": [
      {"name": "operator-health", "status": "passed", "duration": 1000, "suite": ["deployment"]},
      {"name": "check-nvidia-smi", "status": "failed", "duration": 2000, "suite": ["deployment"],
       "message": "nvidia-smi returned 9"}
    ]
  }
}`

const conformanceCTRF = `{
  "reportFormat": "CTRF",
  "specVersion": "0.0.1",
  "results": {
    "tool": {"name": "aicr"},
    "summary": {"tests": 1},
    "tests": [
      {"name": "dra-support", "status": "skipped", "suite": ["conformance"]}
    ]
  }
}`

func TestParseCTRFExtractsNameStatusSuiteAndMessage(t *testing.T) {
	t.Parallel()

	run, err := ParseCTRF([]byte(deploymentCTRF))
	require.NoError(t, err)
	require.Len(t, run.Tests, 2)

	assert.Equal(t, "operator-health", run.Tests[0].Name)
	assert.Equal(t, "passed", run.Tests[0].Status)
	assert.Equal(t, "deployment", run.Tests[0].Suite)

	assert.Equal(t, "check-nvidia-smi", run.Tests[1].Name)
	assert.Equal(t, "failed", run.Tests[1].Status)
	assert.Equal(t, "nvidia-smi returned 9", run.Tests[1].Message,
		"the failure message is the only diagnostic a reader gets, it must survive parsing")
}

func TestParseCTRFRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := ParseCTRF([]byte(`{"results": `))

	require.Error(t, err)
}

// A report that parses but carries no tests must not silently look like a
// successful empty run.
func TestParseCTRFRejectsReportWithNoTests(t *testing.T) {
	t.Parallel()

	_, err := ParseCTRF([]byte(`{"reportFormat":"CTRF","results":{"tests":[]}}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tests")
}

func TestLoadCTRFDirMergesEveryPhaseReport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deployment.json"), []byte(deploymentCTRF), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conformance.json"), []byte(conformanceCTRF), 0o600))
	// A non-JSON sibling must be ignored rather than break the load.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600))

	run, err := LoadCTRFDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(run.Tests))
	for _, test := range run.Tests {
		names = append(names, test.Name)
	}
	assert.ElementsMatch(t, []string{"operator-health", "check-nvidia-smi", "dra-support"}, names,
		"results from every phase report in the directory must be merged")
}

func TestLoadCTRFDirFailsWhenDirectoryHasNoReports(t *testing.T) {
	t.Parallel()

	_, err := LoadCTRFDir(t.TempDir())

	require.Error(t, err)
}

func TestLoadCatalogRoundTripsAndValidates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "catalog.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
source: test
checks:
  - name: operator-health
    area: operator-install
    phase: deployment
    bucket: A
    rationale: installs the real operator
    evidence: validators/deployment/operator_health.go:32
`), 0o600))

	cat, err := LoadCatalog(path)
	require.NoError(t, err)

	require.Len(t, cat.Checks, 1)
	assert.Equal(t, "operator-health", cat.Checks[0].Name)
	assert.Equal(t, BucketA, cat.Checks[0].Bucket)
	assert.Equal(t, "validators/deployment/operator_health.go:32", cat.Checks[0].Evidence)
}

// Loading must refuse an invalid catalog rather than hand back something that
// produces a plausible but unreviewable roll-up.
func TestLoadCatalogRejectsInvalidCatalog(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "catalog.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
checks:
  - name: broken
    bucket: A
`), 0o600))

	_, err := LoadCatalog(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rationale")
}

// The shipped catalog is a deliverable in its own right, so it must stay valid.
func TestShippedCatalogIsValid(t *testing.T) {
	t.Parallel()

	cat, err := LoadCatalog("catalog.yaml")
	require.NoError(t, err, "the catalog shipped alongside this harness must load and validate")

	assert.NotEmpty(t, cat.Checks)
	for _, check := range cat.Checks {
		assert.NotEmpty(t, check.Evidence,
			"catalog row %q must cite the evidence for its bucket", check.Name)
	}
}
