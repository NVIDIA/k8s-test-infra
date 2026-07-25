// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalogFixture returns a small catalog covering every bucket, so each test can
// scope down to the rows it cares about without re-declaring the shape.
func catalogFixture() *Catalog {
	return &Catalog{
		Checks: []CatalogCheck{
			{Name: "operator-health", Area: "operator-install", Bucket: BucketA, Rationale: "installs the real operator", GPUDependent: true},
			{Name: "gang-scheduling", Area: "scheduling", Bucket: BucketA, Rationale: "real KAI scheduling", GPUDependent: false},
			{Name: "accelerator-metrics", Area: "nvml-surface", Bucket: BucketB, Rationale: "presence-only against static mock values"},
			{Name: "nccl-all-reduce-bw-nvls", Area: "nccl-nvlink-fabric", Bucket: BucketC, Rationale: "needs a real NVLS fabric"},
			{Name: "secure-accelerator-access", Area: "dra", Bucket: BucketG, Rationale: "blocked: MIG surface absent", GapIssue: "#1001"},
		},
	}
}

// The central honesty invariant: a check the run never reported must never be
// recorded as passing. If Classify defaulted a missing check to pass (or to the
// zero value of a type whose zero value is pass), this test fails.
func TestCheckAbsentFromRunIsNotRunNeverPass(t *testing.T) {
	t.Parallel()

	// The run only reported operator-health. Every other catalog row is absent.
	run := &RunResults{Tests: []RunTest{
		{Name: "operator-health", Status: "passed", Suite: "deployment"},
	}}

	records := Classify(catalogFixture(), run, ProvenanceSim)
	byName := indexByName(t, records)

	require.Len(t, records, 5, "every catalog row must produce exactly one record")

	assert.Equal(t, OutcomePass, byName["operator-health"].Outcome,
		"a check the run reported passed must be recorded as pass")

	for _, absent := range []string{"gang-scheduling", "accelerator-metrics", "nccl-all-reduce-bw-nvls", "secure-accelerator-access"} {
		assert.Equal(t, OutcomeNotRun, byName[absent].Outcome,
			"%s was absent from the run and must be recorded not-run", absent)
		assert.NotEqual(t, OutcomePass, byName[absent].Outcome,
			"%s must never be recorded as pass without a run", absent)
	}
}

// A hardware-dependent (bucket C) check that comes back green from a simulated
// run is the exact overclaim this POC exists to prevent, so it must be flagged.
func TestBucketCPassUnderSimIsFlaggedSuspect(t *testing.T) {
	t.Parallel()

	run := &RunResults{Tests: []RunTest{
		{Name: "nccl-all-reduce-bw-nvls", Status: "passed", Suite: "performance"},
		{Name: "operator-health", Status: "passed", Suite: "deployment"},
	}}

	byName := indexByName(t, Classify(catalogFixture(), run, ProvenanceSim))

	assert.True(t, byName["nccl-all-reduce-bw-nvls"].Suspect,
		"a green bucket-C check under sim provenance must be flagged suspect")
	assert.False(t, byName["operator-health"].Suspect,
		"a green bucket-A check is expected and must not be flagged")
}

// The same green bucket-C result is legitimate on real hardware, so the flag
// must key off provenance and not off the bucket alone.
func TestBucketCPassUnderSiliconIsNotSuspect(t *testing.T) {
	t.Parallel()

	run := &RunResults{Tests: []RunTest{
		{Name: "nccl-all-reduce-bw-nvls", Status: "passed", Suite: "performance"},
	}}

	byName := indexByName(t, Classify(catalogFixture(), run, ProvenanceSilicon))

	assert.False(t, byName["nccl-all-reduce-bw-nvls"].Suspect,
		"bucket C passing on silicon is the expected case, not a suspect result")
}

func TestEveryRecordCarriesProvenance(t *testing.T) {
	t.Parallel()

	records := Classify(catalogFixture(), &RunResults{}, ProvenanceSim)
	require.NotEmpty(t, records)

	for _, rec := range records {
		assert.Equal(t, ProvenanceSim, rec.Provenance,
			"record %q must carry the run's provenance", rec.Name)
	}
}

func TestStatusMappingCoversEveryCTRFStatus(t *testing.T) {
	t.Parallel()

	cat := &Catalog{Checks: []CatalogCheck{
		{Name: "a", Bucket: BucketA}, {Name: "b", Bucket: BucketA},
		{Name: "c", Bucket: BucketA}, {Name: "d", Bucket: BucketA},
		{Name: "e", Bucket: BucketA},
	}}
	run := &RunResults{Tests: []RunTest{
		{Name: "a", Status: "passed"},
		{Name: "b", Status: "failed"},
		{Name: "c", Status: "skipped"},
		{Name: "d", Status: "pending"},
		{Name: "e", Status: "other"},
	}}

	byName := indexByName(t, Classify(cat, run, ProvenanceSim))

	assert.Equal(t, OutcomePass, byName["a"].Outcome)
	assert.Equal(t, OutcomeFail, byName["b"].Outcome)
	assert.Equal(t, OutcomeSkip, byName["c"].Outcome)
	// pending and other are both "the suite did not reach a verdict", which is
	// materially different from a pass and must not be laundered into one.
	assert.Equal(t, OutcomeNotRun, byName["d"].Outcome)
	assert.Equal(t, OutcomeNotRun, byName["e"].Outcome)
}

// A result the catalog does not know about must surface loudly rather than be
// dropped, otherwise the catalog silently drifts from the suite it describes.
func TestRunResultAbsentFromCatalogIsReportedAsUncatalogued(t *testing.T) {
	t.Parallel()

	run := &RunResults{Tests: []RunTest{
		{Name: "operator-health", Status: "passed", Suite: "deployment"},
		{Name: "brand-new-check", Status: "passed", Suite: "conformance"},
	}}

	missing := UncataloguedChecks(catalogFixture(), run)

	assert.Equal(t, []string{"brand-new-check"}, missing,
		"a check present in the run but absent from the catalog must be reported")
}

func TestRollUpReportsTodayAndReachableSeparately(t *testing.T) {
	t.Parallel()

	// 2 A, 1 B, 1 C, 1 G out of 5.
	sum := Summarize(Classify(catalogFixture(), &RunResults{}, ProvenanceSim))

	assert.Equal(t, 5, sum.Total)
	assert.Equal(t, 2, sum.ByBucket[BucketA])
	assert.Equal(t, 1, sum.ByBucket[BucketB])
	assert.Equal(t, 1, sum.ByBucket[BucketC])
	assert.Equal(t, 1, sum.ByBucket[BucketG])

	// Today = A/total = 2/5. Reachable = (A+G)/total = 3/5.
	assert.InDelta(t, 40.0, sum.MeaningfulTodayPct, 0.001,
		"today's number is A/total and must not include the closable gaps")
	assert.InDelta(t, 60.0, sum.ReachablePct, 0.001,
		"the reachable number is (A+G)/total")
	assert.Greater(t, sum.ReachablePct, sum.MeaningfulTodayPct)
}

// The headline A number is inflated if control-plane checks that would run on
// any cluster are counted as something Mokka unlocked, so the split has to be
// reported separately.
func TestSummarySplitsMokkaUnlockedAFromGPUIndependentA(t *testing.T) {
	t.Parallel()

	sum := Summarize(Classify(catalogFixture(), &RunResults{}, ProvenanceSim))

	// The fixture has 2 bucket-A rows: operator-health (GPU-dependent) and
	// gang-scheduling (GPU-independent, explicitly CPU-only upstream).
	assert.Equal(t, 1, sum.MokkaUnlockedA,
		"only the GPU-dependent bucket-A check is unlocked by Mokka specifically")
	assert.Equal(t, 1, sum.GPUIndependentA,
		"a GPU-independent bucket-A check must not be credited to Mokka")
	assert.Equal(t, sum.ByBucket[BucketA], sum.MokkaUnlockedA+sum.GPUIndependentA,
		"the split must account for every bucket-A row exactly once")
}

func TestSummarizeOnEmptyCatalogDoesNotDivideByZero(t *testing.T) {
	t.Parallel()

	sum := Summarize(nil)

	assert.Equal(t, 0, sum.Total)
	assert.Zero(t, sum.MeaningfulTodayPct)
	assert.Zero(t, sum.ReachablePct)
}

func TestCatalogValidationRejectsUnknownBucket(t *testing.T) {
	t.Parallel()

	cat := &Catalog{Checks: []CatalogCheck{{Name: "x", Bucket: "Z", Rationale: "r"}}}

	err := cat.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Z", "the error must name the offending bucket")
}

func TestCatalogValidationRejectsDuplicateCheckNames(t *testing.T) {
	t.Parallel()

	cat := &Catalog{Checks: []CatalogCheck{
		{Name: "dup", Bucket: BucketA, Rationale: "r"},
		{Name: "dup", Bucket: BucketB, Rationale: "r"},
	}}

	err := cat.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "dup")
}

// Bucket G is the roadmap claim, so it has to point at a tracked issue or the
// backlog is unauditable.
func TestCatalogValidationRequiresGapIssueForBucketG(t *testing.T) {
	t.Parallel()

	cat := &Catalog{Checks: []CatalogCheck{{Name: "g", Bucket: BucketG, Rationale: "r"}}}

	err := cat.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gapIssue")
}

func TestCatalogValidationRequiresRationale(t *testing.T) {
	t.Parallel()

	cat := &Catalog{Checks: []CatalogCheck{{Name: "x", Bucket: BucketA}}}

	err := cat.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rationale")
}

func TestCatalogValidationAcceptsAWellFormedCatalog(t *testing.T) {
	t.Parallel()

	require.NoError(t, catalogFixture().Validate())
}

func indexByName(t *testing.T, records []Record) map[string]Record {
	t.Helper()

	byName := make(map[string]Record, len(records))
	for _, rec := range records {
		_, dup := byName[rec.Name]
		require.False(t, dup, "duplicate record for %q", rec.Name)
		byName[rec.Name] = rec
	}
	return byName
}
