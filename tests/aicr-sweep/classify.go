// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// statusToOutcome maps a CTRF status onto a sweep outcome.
//
// The mapping is deliberately conservative at both ends. CTRF "pending" and
// "other" mean the suite reached no verdict: "other" is AICR's own code for a
// crash, an OOM, a timeout, or a Job that failed with no inspectable pod. Those
// become OutcomeNotRun rather than being laundered into either a pass or a
// fail. A run that half-finished must look half-finished in the numbers.
func statusToOutcome(status string) Outcome {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed":
		return OutcomePass
	case "failed":
		return OutcomeFail
	case "skipped":
		return OutcomeSkip
	case "pending", "other", "":
		return OutcomeNotRun
	default:
		// An unrecognised status is not a pass. Fail closed.
		return OutcomeNotRun
	}
}

// Classify joins a catalog against one run's results for one cell.
//
// Invariants, each of which exists because breaking it produces a flattering
// number instead of a true one:
//
//  1. A catalog check that the run never reported is OutcomeNotRun. It is never
//     promoted to a pass.
//  2. A check present in the run but absent from the catalog is reported as
//     drift, not silently dropped.
//  3. A bucket-C check reporting a pass under sim provenance is flagged
//     Suspect. A hardware-dependent check cannot legitimately pass without
//     hardware, so a green there means the check is weaker than its name.
//  4. Every emitted record carries provenance.
//
// blockedCause, when not CauseNone, marks the whole cell blocked: every check
// becomes OutcomeBlocked carrying that cause. This is how a cell that never ran
// stays visibly distinct from a cell that ran and passed.
//
//nolint:cyclop // existing complexity; refactor deferred
func Classify(cat *Catalog, run *RunResults, cell Cell, versions Versions, prov Provenance, blockedCause Cause, blockedWhy string) CellResult {
	result := CellResult{
		Cell:       cell,
		Versions:   versions,
		Provenance: prov,
		Checks:     make([]CheckResult, 0, len(cat.Checks)),
	}

	if blockedCause != CauseNone {
		result.Status = "blocked"
		result.Cause = blockedCause
		result.BlockedWhy = blockedWhy
		for _, c := range cat.Checks {
			result.Checks = append(result.Checks, CheckResult{
				Check: c.Name, Area: c.Area, Phase: c.Phase,
				Bucket: c.Bucket, GPUDep: c.GPUDependent,
				Outcome: OutcomeBlocked, Cause: blockedCause,
				Provenance: prov, GapIssue: c.GapIssue,
				Message: blockedWhy,
			})
		}
		return result
	}

	byName := map[string]RunTest{}
	if run != nil {
		for _, t := range run.Tests {
			byName[t.Name] = t
		}
	}

	seen := map[string]bool{}
	for _, c := range cat.Checks {
		cr := CheckResult{
			Check: c.Name, Area: c.Area, Phase: c.Phase,
			Bucket: c.Bucket, GPUDep: c.GPUDependent,
			Provenance: prov, GapIssue: c.GapIssue,
		}

		t, ok := byName[c.Name]
		if !ok {
			// Invariant 1: absent from the run means no verdict, not a pass.
			cr.Outcome = OutcomeNotRun
			cr.Note = "check absent from run output"
			result.Checks = append(result.Checks, cr)
			continue
		}
		seen[c.Name] = true

		cr.Outcome = statusToOutcome(t.Status)
		cr.Message = t.Message

		// Invariant 3: a hardware-dependent check cannot honestly pass without
		// hardware. Flag it rather than counting it.
		if c.Bucket == BucketC && cr.Outcome == OutcomePass && prov == ProvenanceSim {
			cr.Suspect = true
			cr.Note = "bucket C passed under sim provenance: the check does not actually exercise the hardware path it names"
		}

		result.Checks = append(result.Checks, cr)
	}

	// Invariant 2: report drift instead of dropping it.
	for name := range byName {
		if !seen[name] {
			result.Drift = append(result.Drift, name)
		}
	}
	sort.Strings(result.Drift)

	result.Status = "ran"
	return result
}

// Roll aggregates check results across cells into the coverage read.
//
// The denominator is every catalog check in every cell, including blocked and
// not-run ones. Dropping them would raise the percentage by hiding the work
// that did not happen, which is the specific dishonesty this sweep exists to
// avoid.
//
//nolint:cyclop // existing complexity; refactor deferred
func Roll(cells []CellResult) Rollup {
	r := Rollup{ByCause: map[Cause]int{}, ByOutcome: map[Outcome]int{}}
	for _, cell := range cells {
		for _, c := range cell.Checks {
			r.Total++
			r.ByOutcome[c.Outcome]++
			if c.Cause != CauseNone {
				r.ByCause[c.Cause]++
			}
			if c.Suspect {
				r.Suspect++
			}
			if c.Outcome == OutcomeBlocked || c.Outcome == OutcomeNotRun {
				r.Blocked++
			}
			switch c.Bucket {
			case BucketA:
				r.A++
				if c.GPUDep {
					r.AGPUDependent++
				}
			case BucketB:
				r.B++
			case BucketC:
				r.C++
			case BucketG:
				r.G++
			}
		}
	}
	return r
}

// Validate rejects a catalog that would produce a misleading number.
//
//nolint:cyclop // existing complexity; refactor deferred
func (c *Catalog) Validate() error {
	if len(c.Checks) == 0 {
		return errors.New("catalog has no checks")
	}
	if strings.TrimSpace(c.Source) == "" {
		return errors.New("catalog has no source ref: a catalog whose provenance is unknown cannot be audited")
	}

	seen := map[string]bool{}
	for i, ch := range c.Checks {
		if strings.TrimSpace(ch.Name) == "" {
			return fmt.Errorf("check %d has no name", i)
		}
		if seen[ch.Name] {
			return fmt.Errorf("check %q appears twice: a duplicate double-counts in the rollup", ch.Name)
		}
		seen[ch.Name] = true

		switch ch.Bucket {
		case BucketA, BucketB, BucketC, BucketG:
		default:
			return fmt.Errorf("check %q has invalid bucket %q, want one of A B C G", ch.Name, ch.Bucket)
		}

		if strings.TrimSpace(ch.Rationale) == "" {
			return fmt.Errorf("check %q has no rationale: an unexplained bucket cannot be reviewed", ch.Name)
		}
		if strings.TrimSpace(ch.Evidence) == "" {
			return fmt.Errorf("check %q has no evidence ref: a bucket with no source citation is an assertion", ch.Name)
		}

		// A bucket-G check without a tracked issue is not a roadmap, it is a
		// claim. Reject it.
		if ch.Bucket == BucketG && strings.TrimSpace(ch.GapIssue) == "" {
			return fmt.Errorf("check %q is bucket G but has no gapIssue: an untracked gap is not a roadmap", ch.Name)
		}
	}
	return nil
}
