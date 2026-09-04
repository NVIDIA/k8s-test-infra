// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

// Bucket is the analytical judgment about what an AICR check proves when it runs
// against a simulated cluster. The bucket comes from source evidence and is
// reviewable independently of any run. The harness never edits a bucket from an
// observed outcome, because a classification that moves to match the result is
// not a classification.
type Bucket string

const (
	// BucketA exercises real integration. Could plausibly fail for a real reason.
	BucketA Bucket = "A"
	// BucketB is green only because the mock answers the API. It would pass
	// regardless of correctness, so it is not coverage. Recorded honestly
	// because it bounds the claim.
	BucketB Bucket = "B"
	// BucketC is hardware-dependent and cannot be judged pre-silicon. These are
	// why AICR-on-silicon remains the final readiness gate.
	BucketC Bucket = "C"
	// BucketG would be meaningful pre-silicon, but Mokka lacks the capability.
	// Not hardware-dependent, therefore closable, therefore roadmap. Needs an
	// issue.
	BucketG Bucket = "G"
)

// Cause explains why a cell did not produce a usable verdict. It is deliberately
// a separate axis from Bucket: a bucket says what a check would prove, a cause
// says what stopped this particular run. Keeping them separate is what stops a
// kind artifact from being reported as a Mokka gap.
type Cause string

const (
	// CauseNone means the cell ran and reached a verdict.
	CauseNone Cause = ""
	// CauseK is a kind or single-host environment artifact. The check broke for
	// a reason that would not occur on a real cluster. NOT a Mokka finding.
	CauseK Cause = "K"
	// CauseU is an upstream dependency gap: the capability exists in Mokka but
	// a dependency has not released what is needed to consume it.
	CauseU Cause = "U"
	// CauseX is an AICR catalog gap: AICR has no recipe or accelerator for the
	// requested combination, so there is nothing to run. Not a Mokka finding.
	CauseX Cause = "X"
	// CauseBudget means the cell was not attempted because the host ran out of
	// memory budget. Recorded so the denominator stays honest.
	CauseBudget Cause = "BUDGET"
)

// Outcome is the observed result of one check in one cell.
type Outcome string

const (
	// OutcomePass means the suite reported a pass.
	OutcomePass Outcome = "pass"
	// OutcomeFail means the suite reported a failure.
	OutcomeFail Outcome = "fail"
	// OutcomeSkip means the suite deliberately skipped the check.
	OutcomeSkip Outcome = "skip"
	// OutcomeNotRun means no verdict was reached. A check absent from a run, or
	// reported CTRF "pending" or "other", lands here. It is NEVER promoted to a
	// pass: an incomplete run must degrade to "not run", not to a good number.
	OutcomeNotRun Outcome = "not-run"
	// OutcomeBlocked means the cell could not run at all. Carries a Cause.
	OutcomeBlocked Outcome = "blocked"
)

// Provenance records whether evidence came from simulation or from silicon. The
// two are never conflated, and every emitted record carries one.
type Provenance string

const (
	// ProvenanceSim means the evidence came from a simulated GPU cluster.
	ProvenanceSim Provenance = "sim"
	// ProvenanceSilicon means the evidence came from real hardware.
	ProvenanceSilicon Provenance = "silicon"
)

// Check is one AICR validation or conformance check, with the analytical bucket
// it falls into when run against Mokka.
type Check struct {
	Name string `json:"name" yaml:"name"`
	Area string `json:"area" yaml:"area"`
	// Phase is the AICR validate phase: deployment, performance or conformance.
	Phase string `json:"phase" yaml:"phase"`
	// Bucket is the analytical classification. See Bucket.
	Bucket Bucket `json:"bucket" yaml:"bucket"`
	// GPUDependent records whether the check actually touches the GPU stack. A
	// bucket-A check with GPUDependent false still moves left, but ANY cluster
	// runs it, so Mokka is not what unlocks it. Keeping the split visible stops
	// the headline A number from being inflated by pure control-plane checks.
	GPUDependent bool `json:"gpuDependent" yaml:"gpuDependent"`
	// Rationale states why this bucket, in prose a reviewer can attack.
	Rationale string `json:"rationale" yaml:"rationale"`
	// Evidence is the file:line in NVIDIA/aicr backing the rationale.
	Evidence string `json:"evidence" yaml:"evidence"`
	// GapIssue is the tracked issue URL for a bucket-G check. A bucket-G check
	// without one fails catalog validation: an untracked gap is not a roadmap.
	GapIssue string `json:"gapIssue,omitempty" yaml:"gapIssue,omitempty"`
}

// Catalog is the full analytical classification of the AICR check suite.
type Catalog struct {
	// Source identifies the AICR ref the catalog was derived from, so a reader
	// can tell when it has drifted.
	Source string  `json:"source" yaml:"source"`
	Checks []Check `json:"checks" yaml:"checks"`
}

// RunTest is one check outcome as the AICR suite reported it.
type RunTest struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Suite   string `json:"suite"`
	Message string `json:"message"`
}

// RunResults is one AICR validate run, merged across phase reports.
type RunResults struct {
	Tests []RunTest `json:"tests"`
}

// Versions is the full version set recorded on every cell. Without it the
// numbers are not reproducible.
type Versions struct {
	Mokka         string `json:"mokka"`
	MokkaImage    string `json:"mokkaImage"`
	MokkaDigest   string `json:"mokkaDigest,omitempty"`
	Chart         string `json:"chart"`
	ChartDigest   string `json:"chartDigest,omitempty"`
	Profile       string `json:"profile"`
	Kind          string `json:"kind"`
	NodeImage     string `json:"nodeImage"`
	Kubernetes    string `json:"kubernetes"`
	GPUOperator   string `json:"gpuOperator,omitempty"`
	DRADriver     string `json:"draDriver,omitempty"`
	DCGMExporter  string `json:"dcgmExporter,omitempty"`
	AICR          string `json:"aicr"`
	AICRImage     string `json:"aicrImage,omitempty"`
	HostArch      string `json:"hostArch"`
	HostContainer string `json:"hostContainerRuntime,omitempty"`
}

// Cell is one point in the permutation matrix.
type Cell struct {
	// ID is a stable identifier, used as the results filename.
	ID string `json:"id"`
	// Recipe is the AICR recipe criteria that generated the recipe document.
	Recipe RecipeCriteria `json:"recipe"`
	// Shape is the cluster shape: "stack" or "fabric".
	Shape string `json:"shape"`
	// Workers is the worker node count.
	Workers int `json:"workers"`
	// Axes records which axis this cell varies off the base, and to what.
	Axes map[string]string `json:"axes,omitempty"`
	// Notes carries anything a reader needs to interpret the cell.
	Notes string `json:"notes,omitempty"`
}

// RecipeCriteria is the input to `aicr recipe`.
type RecipeCriteria struct {
	Service     string `json:"service"`
	Accelerator string `json:"accelerator"`
	Intent      string `json:"intent"`
	OS          string `json:"os,omitempty"`
	Platform    string `json:"platform,omitempty"`
}

// CheckResult joins one catalog check with its observed outcome in one cell.
type CheckResult struct {
	Check      string     `json:"check"`
	Area       string     `json:"area"`
	Phase      string     `json:"phase"`
	Bucket     Bucket     `json:"bucket"`
	GPUDep     bool       `json:"gpuDependent"`
	Outcome    Outcome    `json:"outcome"`
	Cause      Cause      `json:"cause,omitempty"`
	Provenance Provenance `json:"provenance"`
	Message    string     `json:"message,omitempty"`
	GapIssue   string     `json:"gapIssue,omitempty"`
	// Suspect flags a result that contradicts its own classification: a
	// hardware-dependent check reporting a pass without hardware. It is not an
	// error, it is a signal that the check is weaker than its name suggests.
	Suspect bool   `json:"suspect,omitempty"`
	Note    string `json:"note,omitempty"`
}

// CellResult is the machine-readable record for one matrix cell.
type CellResult struct {
	Cell        Cell          `json:"cell"`
	Versions    Versions      `json:"versions"`
	Provenance  Provenance    `json:"provenance"`
	Status      string        `json:"status"`
	Cause       Cause         `json:"cause,omitempty"`
	BlockedWhy  string        `json:"blockedWhy,omitempty"`
	LogsPointer string        `json:"logsPointer,omitempty"`
	StartedAt   string        `json:"startedAt,omitempty"`
	Checks      []CheckResult `json:"checks"`
	// Drift lists checks present in the run but absent from the catalog. They
	// are reported rather than silently dropped.
	Drift []string `json:"drift,omitempty"`
}

// Rollup is the aggregate coverage read across a set of cells.
type Rollup struct {
	Total int `json:"total"`
	A     int `json:"a"`
	B     int `json:"b"`
	C     int `json:"c"`
	G     int `json:"g"`
	// AGPUDependent is the subset of A that actually touches the GPU stack.
	// This is the number Mokka can claim credit for.
	AGPUDependent int `json:"aGpuDependent"`
	// Blocked counts checks that never reached a verdict, by cause.
	Blocked   int             `json:"blocked"`
	ByCause   map[Cause]int   `json:"byCause,omitempty"`
	ByOutcome map[Outcome]int `json:"byOutcome,omitempty"`
	Suspect   int             `json:"suspect"`
}
