// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command aicr-preflight joins an AICR validate run against the Mokka coverage
// catalog and emits a provenance-labelled, bucketed result set.
//
// The governing invariant is that a check the run did not report can never be
// recorded as passing. Outcomes are only ever promoted by an observed result,
// so an incomplete run degrades to "not run" rather than to a favourable
// number. See README.md for the bucket definitions.
package main

import (
	"errors"
	"fmt"
	"sort"
)

// Bucket classifies what an AICR check actually proves when it runs against a
// Mokka-simulated cluster rather than against silicon.
type Bucket string

const (
	// BucketA exercises real integration logic and could plausibly fail for a
	// real reason. This is the movable-left set.
	BucketA Bucket = "A"
	// BucketB is green only because the mock answers the API. No real signal.
	// Recording it honestly is what bounds the claim.
	BucketB Bucket = "B"
	// BucketC is hardware-dependent and cannot be judged pre-silicon.
	BucketC Bucket = "C"
	// BucketG could be meaningful pre-silicon but Mokka lacks the capability.
	// Not hardware-dependent, therefore closable, therefore roadmap.
	BucketG Bucket = "G"
)

// Provenance records whether the evidence came from simulation or from silicon.
// It is attached to every emitted record so the two can never be conflated.
type Provenance string

const (
	ProvenanceSim     Provenance = "sim"
	ProvenanceSilicon Provenance = "silicon"
)

// Outcome is what the run observed. The zero value is deliberately OutcomeNotRun
// so that a missing result can never read as a pass.
type Outcome string

const (
	OutcomeNotRun Outcome = "not-run"
	OutcomePass   Outcome = "pass"
	OutcomeFail   Outcome = "fail"
	OutcomeSkip   Outcome = "skip"
)

// CatalogCheck is one row of the coverage catalog: the analytical judgment about
// an AICR check, made from evidence and reviewable independently of any run.
type CatalogCheck struct {
	Name      string `json:"name" yaml:"name"`
	Area      string `json:"area,omitempty" yaml:"area,omitempty"`
	Phase     string `json:"phase,omitempty" yaml:"phase,omitempty"`
	Bucket    Bucket `json:"bucket" yaml:"bucket"`
	Rationale string `json:"rationale" yaml:"rationale"`
	// GPUDependent records whether the check actually depends on the GPU stack.
	// A bucket-A check that is GPU-independent still moves left, but any cluster
	// would run it, so Mokka is not what unlocks it. Tracking this separately
	// stops the headline A number from being inflated by control-plane checks
	// that have nothing to do with GPU simulation.
	GPUDependent bool `json:"gpuDependent" yaml:"gpuDependent"`
	// GapIssue links the tracked issue for a bucket-G row. Required for G so the
	// roadmap claim is auditable.
	GapIssue string `json:"gapIssue,omitempty" yaml:"gapIssue,omitempty"`
	// Evidence cites the source that justifies the bucket, as file:line or URL.
	Evidence string `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

// Catalog is the full set of AICR checks under consideration.
type Catalog struct {
	Source string         `json:"source,omitempty" yaml:"source,omitempty"`
	Checks []CatalogCheck `json:"checks" yaml:"checks"`
}

// Validate rejects a catalog that cannot be trusted to produce an honest
// roll-up: unknown buckets, duplicates, unexplained rows, or a bucket-G claim
// with no tracked issue behind it.
func (c *Catalog) Validate() error {
	var errs []error
	seen := make(map[string]struct{}, len(c.Checks))

	for i, check := range c.Checks {
		if check.Name == "" {
			errs = append(errs, fmt.Errorf("checks[%d]: name must not be empty", i))
			continue
		}
		if _, dup := seen[check.Name]; dup {
			errs = append(errs, fmt.Errorf("checks[%d]: duplicate check name %q", i, check.Name))
		}
		seen[check.Name] = struct{}{}

		switch check.Bucket {
		case BucketA, BucketB, BucketC, BucketG:
		default:
			errs = append(errs, fmt.Errorf("checks[%d] (%s): unknown bucket %q, want one of A, B, C, G",
				i, check.Name, check.Bucket))
		}

		if check.Rationale == "" {
			errs = append(errs, fmt.Errorf("checks[%d] (%s): rationale must not be empty, a bucket without a stated mechanism is not reviewable",
				i, check.Name))
		}

		if check.Bucket == BucketG && check.GapIssue == "" {
			errs = append(errs, fmt.Errorf("checks[%d] (%s): bucket G requires a gapIssue, an untracked gap is not a roadmap",
				i, check.Name))
		}
	}

	return errors.Join(errs...)
}

// RunTest is one observed result from an AICR CTRF report.
type RunTest struct {
	Name    string
	Status  string
	Suite   string
	Message string
}

// RunResults is the set of results parsed from one or more CTRF reports.
type RunResults struct {
	Tests []RunTest
}

// Record is one catalog row joined with what the run actually observed.
type Record struct {
	Name         string     `json:"name"`
	Area         string     `json:"area,omitempty"`
	Phase        string     `json:"phase,omitempty"`
	Bucket       Bucket     `json:"bucket"`
	Outcome      Outcome    `json:"outcome"`
	Provenance   Provenance `json:"provenance"`
	Rationale    string     `json:"rationale"`
	GPUDependent bool       `json:"gpuDependent"`
	GapIssue     string     `json:"gapIssue,omitempty"`
	Evidence     string     `json:"evidence,omitempty"`
	Message      string     `json:"message,omitempty"`
	// Suspect marks a result that contradicts its bucket badly enough to need a
	// human look, specifically a hardware-dependent check reporting green from a
	// simulated run.
	Suspect bool `json:"suspect,omitempty"`
}

// outcomeFor maps a CTRF status onto an Outcome. Anything that is not an
// explicit verdict degrades to OutcomeNotRun; "pending" and "other" mean the
// suite reached no conclusion and must not be laundered into a pass.
func outcomeFor(status string) Outcome {
	switch status {
	case "passed":
		return OutcomePass
	case "failed":
		return OutcomeFail
	case "skipped":
		return OutcomeSkip
	default:
		return OutcomeNotRun
	}
}

// Classify joins every catalog row with the run, in catalog order. A catalog row
// with no corresponding run result is recorded OutcomeNotRun; it is never
// promoted to a pass.
func Classify(catalog *Catalog, run *RunResults, provenance Provenance) []Record {
	if catalog == nil {
		return nil
	}

	observed := make(map[string]RunTest)
	if run != nil {
		for _, test := range run.Tests {
			observed[test.Name] = test
		}
	}

	records := make([]Record, 0, len(catalog.Checks))
	for _, check := range catalog.Checks {
		rec := Record{
			Name:         check.Name,
			Area:         check.Area,
			Phase:        check.Phase,
			Bucket:       check.Bucket,
			Outcome:      OutcomeNotRun,
			Provenance:   provenance,
			Rationale:    check.Rationale,
			GPUDependent: check.GPUDependent,
			GapIssue:     check.GapIssue,
			Evidence:     check.Evidence,
		}

		if test, ran := observed[check.Name]; ran {
			rec.Outcome = outcomeFor(test.Status)
			rec.Message = test.Message
		}

		// A hardware-dependent check cannot legitimately go green without
		// hardware. Flag it rather than counting it.
		rec.Suspect = check.Bucket == BucketC &&
			rec.Outcome == OutcomePass &&
			provenance == ProvenanceSim

		records = append(records, rec)
	}

	return records
}

// UncataloguedChecks returns run results with no catalog row, sorted. These are
// catalog drift: the suite grew a check the catalog does not describe.
func UncataloguedChecks(catalog *Catalog, run *RunResults) []string {
	if run == nil {
		return nil
	}

	known := make(map[string]struct{})
	if catalog != nil {
		for _, check := range catalog.Checks {
			known[check.Name] = struct{}{}
		}
	}

	var missing []string
	seen := make(map[string]struct{})
	for _, test := range run.Tests {
		if _, ok := known[test.Name]; ok {
			continue
		}
		if _, dup := seen[test.Name]; dup {
			continue
		}
		seen[test.Name] = struct{}{}
		missing = append(missing, test.Name)
	}

	sort.Strings(missing)
	return missing
}

// Summary is the roll-up. It deliberately carries both percentages so the
// reachable number can never be presented as the current one.
type Summary struct {
	Total    int             `json:"total"`
	ByBucket map[Bucket]int  `json:"byBucket"`
	ByOutcom map[Outcome]int `json:"byOutcome"`
	// MeaningfulTodayPct is A/total: the share carrying real pre-silicon signal
	// today. The honest current state.
	MeaningfulTodayPct float64 `json:"meaningfulTodayPct"`
	// ReachablePct is (A+G)/total: the share reachable once the tracked gaps
	// close. Roadmap, never a current claim.
	ReachablePct float64 `json:"reachablePct"`
	SuspectCount int     `json:"suspectCount"`
	// MokkaUnlockedA counts bucket-A checks that actually depend on the GPU
	// stack. This is the subset Mokka specifically enables; the remainder of A
	// would run on any cluster.
	MokkaUnlockedA int `json:"mokkaUnlockedA"`
	// GPUIndependentA counts bucket-A checks that need no GPU stack at all.
	GPUIndependentA int `json:"gpuIndependentA"`
}

// Summarize computes the roll-up over classified records.
func Summarize(records []Record) Summary {
	sum := Summary{
		Total:    len(records),
		ByBucket: map[Bucket]int{BucketA: 0, BucketB: 0, BucketC: 0, BucketG: 0},
		ByOutcom: map[Outcome]int{},
	}

	for _, rec := range records {
		sum.ByBucket[rec.Bucket]++
		sum.ByOutcom[rec.Outcome]++
		if rec.Suspect {
			sum.SuspectCount++
		}
		if rec.Bucket == BucketA {
			if rec.GPUDependent {
				sum.MokkaUnlockedA++
			} else {
				sum.GPUIndependentA++
			}
		}
	}

	if sum.Total > 0 {
		total := float64(sum.Total)
		sum.MeaningfulTodayPct = float64(sum.ByBucket[BucketA]) / total * 100
		sum.ReachablePct = float64(sum.ByBucket[BucketA]+sum.ByBucket[BucketG]) / total * 100
	}

	return sum
}
