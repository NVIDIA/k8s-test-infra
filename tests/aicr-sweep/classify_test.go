// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCatalog() *Catalog {
	return &Catalog{
		Source: "test",
		Checks: []Check{
			{Name: "a-gpu", Area: "nvml", Phase: "deployment", Bucket: BucketA, GPUDependent: true, Rationale: "r", Evidence: "e"},
			{Name: "a-cpu", Area: "cp", Phase: "conformance", Bucket: BucketA, GPUDependent: false, Rationale: "r", Evidence: "e"},
			{Name: "b-one", Area: "tel", Phase: "conformance", Bucket: BucketB, GPUDependent: true, Rationale: "r", Evidence: "e"},
			{Name: "c-one", Area: "nccl", Phase: "performance", Bucket: BucketC, GPUDependent: true, Rationale: "r", Evidence: "e"},
		},
	}
}

func find(t *testing.T, res CellResult, name string) CheckResult {
	t.Helper()
	for _, c := range res.Checks {
		if c.Check == name {
			return c
		}
	}
	t.Fatalf("check %q not found in result", name)
	return CheckResult{}
}

// The single most important invariant: a check the run never mentioned must not
// become a pass. If this regresses, an incomplete run reports a good number.
func TestCheckAbsentFromRunIsNotRunNeverPass(t *testing.T) {
	t.Parallel()

	run := &RunResults{Tests: []RunTest{{Name: "a-gpu", Status: "passed"}}}
	res := Classify(testCatalog(), run, Cell{ID: "x"}, Versions{}, ProvenanceSim, CauseNone, "")

	assert.Equal(t, OutcomePass, find(t, res, "a-gpu").Outcome, "a reported pass must stay a pass")

	for _, absent := range []string{"a-cpu", "b-one", "c-one"} {
		got := find(t, res, absent)
		assert.Equal(t, OutcomeNotRun, got.Outcome, "check %q was absent from the run and must be not-run", absent)
		assert.NotEqual(t, OutcomePass, got.Outcome, "check %q must never be promoted to a pass", absent)
	}
}

// CTRF "pending" and "other" mean the suite reached no verdict. AICR uses
// "other" for a crash, an OOM, or a timeout. Laundering either into a pass, or
// into a fail, would misreport what happened.
func TestPendingAndOtherBecomeNotRun(t *testing.T) {
	t.Parallel()

	cases := map[string]Outcome{
		"passed":    OutcomePass,
		"failed":    OutcomeFail,
		"skipped":   OutcomeSkip,
		"pending":   OutcomeNotRun,
		"other":     OutcomeNotRun,
		"":          OutcomeNotRun,
		"nonsense":  OutcomeNotRun,
		" PASSED  ": OutcomePass,
	}
	for status, want := range cases {
		assert.Equal(t, want, statusToOutcome(status), "status %q", status)
	}
}

// A hardware-dependent check cannot legitimately pass without hardware. When it
// reports green under sim provenance, that means the check is weaker than its
// name, and the report must say so rather than bank the pass.
func TestBucketCPassUnderSimIsFlaggedSuspect(t *testing.T) {
	t.Parallel()

	run := &RunResults{Tests: []RunTest{{Name: "c-one", Status: "passed"}}}
	res := Classify(testCatalog(), run, Cell{ID: "x"}, Versions{}, ProvenanceSim, CauseNone, "")
	got := find(t, res, "c-one")

	assert.True(t, got.Suspect, "bucket C passing under sim must be flagged suspect")
	assert.Contains(t, got.Note, "bucket C")

	// The same result on silicon is legitimate and must NOT be flagged.
	resSilicon := Classify(testCatalog(), run, Cell{ID: "x"}, Versions{}, ProvenanceSilicon, CauseNone, "")
	assert.False(t, find(t, resSilicon, "c-one").Suspect, "bucket C passing on silicon is legitimate")
}

// A blocked cell must not silently disappear from the denominator, and every
// check in it must carry the cause so K never gets read as G.
func TestBlockedCellMarksEveryCheckWithCause(t *testing.T) {
	t.Parallel()

	res := Classify(testCatalog(), nil, Cell{ID: "x"}, Versions{}, ProvenanceSim, CauseX, "AICR has no gb300 accelerator")

	assert.Equal(t, "blocked", res.Status)
	assert.Equal(t, CauseX, res.Cause)
	require.Len(t, res.Checks, 4, "a blocked cell still enumerates every catalog check")
	for _, c := range res.Checks {
		assert.Equal(t, OutcomeBlocked, c.Outcome, "check %q", c.Check)
		assert.Equal(t, CauseX, c.Cause, "check %q must carry the blocking cause", c.Check)
		assert.NotEqual(t, CauseG(), c.Cause, "a blocked-by-AICR cell must never be attributed to a Mokka gap")
	}
}

// CauseG does not exist as a Cause on purpose: G is a bucket, not a cause. This
// helper exists so the assertion above reads clearly and fails to compile if
// someone later adds a CauseG that would blur the two axes.
func CauseG() Cause { return Cause("G") }

// A check the run reported but the catalog does not know about must surface as
// drift. Dropping it would hide a catalog that has fallen behind AICR.
func TestUnknownCheckInRunIsReportedAsDrift(t *testing.T) {
	t.Parallel()

	run := &RunResults{Tests: []RunTest{
		{Name: "a-gpu", Status: "passed"},
		{Name: "brand-new-check", Status: "failed"},
	}}
	res := Classify(testCatalog(), run, Cell{ID: "x"}, Versions{}, ProvenanceSim, CauseNone, "")

	assert.Equal(t, []string{"brand-new-check"}, res.Drift)
}

// The rollup denominator must include blocked and not-run results. Excluding
// them would raise the coverage percentage by hiding the work that did not
// happen.
func TestRollupDenominatorIncludesBlockedAndNotRun(t *testing.T) {
	t.Parallel()

	ran := Classify(testCatalog(), &RunResults{Tests: []RunTest{{Name: "a-gpu", Status: "passed"}}},
		Cell{ID: "ran"}, Versions{}, ProvenanceSim, CauseNone, "")
	blocked := Classify(testCatalog(), nil, Cell{ID: "blocked"}, Versions{}, ProvenanceSim, CauseK, "kind artifact")

	roll := Roll([]CellResult{ran, blocked})

	assert.Equal(t, 8, roll.Total, "two cells of four checks each must give a denominator of 8")
	assert.Equal(t, 4, roll.A, "two A checks per cell across two cells")
	assert.Equal(t, 2, roll.AGPUDependent, "only one A check per cell is GPU-dependent")
	assert.Equal(t, 7, roll.Blocked, "one pass; the other seven results reached no verdict")
	assert.Equal(t, 4, roll.ByCause[CauseK])
}

func TestPctZeroDenominatorIsNotAPercentage(t *testing.T) {
	t.Parallel()

	// 0% reads as a measured result; n/a does not.
	assert.Equal(t, "n/a", pct(0, 0))
	assert.Equal(t, "50%", pct(1, 2))
}

// An untracked gap is a claim, not a roadmap. The catalog must refuse it.
func TestCatalogRejectsBucketGWithoutIssue(t *testing.T) {
	t.Parallel()

	cat := &Catalog{Source: "s", Checks: []Check{
		{Name: "g1", Bucket: BucketG, Rationale: "r", Evidence: "e"},
	}}
	err := cat.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gapIssue")

	cat.Checks[0].GapIssue = "https://github.com/NVIDIA/k8s-test-infra/issues/1"
	assert.NoError(t, cat.Validate())
}

func TestCatalogRejectsMalformedEntries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cat  Catalog
		want string
	}{
		{"no source", Catalog{Checks: []Check{{Name: "a", Bucket: BucketA, Rationale: "r", Evidence: "e"}}}, "source"},
		{"no checks", Catalog{Source: "s"}, "no checks"},
		{"bad bucket", Catalog{Source: "s", Checks: []Check{{Name: "a", Bucket: "Z", Rationale: "r", Evidence: "e"}}}, "invalid bucket"},
		{"no rationale", Catalog{Source: "s", Checks: []Check{{Name: "a", Bucket: BucketA, Evidence: "e"}}}, "rationale"},
		{"no evidence", Catalog{Source: "s", Checks: []Check{{Name: "a", Bucket: BucketA, Rationale: "r"}}}, "evidence"},
		{"duplicate", Catalog{Source: "s", Checks: []Check{
			{Name: "a", Bucket: BucketA, Rationale: "r", Evidence: "e"},
			{Name: "a", Bucket: BucketA, Rationale: "r", Evidence: "e"},
		}}, "twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cat.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// An empty CTRF report and a clean run must not look alike.
func TestEmptyCTRFReportIsAnError(t *testing.T) {
	t.Parallel()

	_, err := ParseCTRF([]byte(`{"reportFormat":"CTRF","results":{"tests":[]}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tests")
}

func TestParseCTRFReadsPhaseFromSuite(t *testing.T) {
	t.Parallel()

	run, err := ParseCTRF([]byte(`{"reportFormat":"CTRF","results":{"tests":[
	  {"name":"operator-health","status":"passed","suite":["deployment"],"message":"ok"}]}}`))
	require.NoError(t, err)
	require.Len(t, run.Tests, 1)
	assert.Equal(t, "operator-health", run.Tests[0].Name)
	assert.Equal(t, "deployment", run.Tests[0].Suite)
	assert.Equal(t, "passed", run.Tests[0].Status)
}

// The real shipped catalog must satisfy its own validation rules.
func TestShippedCatalogIsValid(t *testing.T) {
	t.Parallel()

	cat, err := LoadCatalog("catalog.yaml")
	require.NoError(t, err)
	assert.Len(t, cat.Checks, 21, "AICR @0752ea14 defines 21 validator checks")

	byPhase := map[string]int{}
	for _, c := range cat.Checks {
		byPhase[c.Phase]++
	}
	assert.Equal(t, 4, byPhase["deployment"])
	assert.Equal(t, 4, byPhase["performance"])
	assert.Equal(t, 13, byPhase["conformance"])
}
