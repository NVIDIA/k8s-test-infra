// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Report is the machine-readable output. Every record carries provenance, and
// the roll-up carries both the current and the reachable number so the second
// can never be quoted as the first.
type Report struct {
	Schema string `json:"schema"`
	// GeneratedAt is supplied by the caller rather than read from the clock, so
	// the report is reproducible in tests.
	GeneratedAt  string     `json:"generatedAt"`
	Provenance   Provenance `json:"provenance"`
	CatalogSrc   string     `json:"catalogSource,omitempty"`
	Cluster      string     `json:"cluster,omitempty"`
	Profile      string     `json:"profile,omitempty"`
	Summary      Summary    `json:"summary"`
	Records      []Record   `json:"records"`
	Uncatalogued []string   `json:"uncatalogued,omitempty"`
}

// BuildReport assembles the report from a classified run.
func BuildReport(catalog *Catalog, run *RunResults, provenance Provenance, cluster, profile string, at time.Time) Report {
	records := Classify(catalog, run, provenance)

	report := Report{
		Schema:       "nvidia.com/aicr-preflight/v1alpha1",
		GeneratedAt:  at.UTC().Format(time.RFC3339),
		Provenance:   provenance,
		Cluster:      cluster,
		Profile:      profile,
		Summary:      Summarize(records),
		Records:      records,
		Uncatalogued: UncataloguedChecks(catalog, run),
	}
	if catalog != nil {
		report.CatalogSrc = catalog.Source
	}

	return report
}

// WriteJSON emits the machine-readable report.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return nil
}

// WriteMarkdown emits the human-readable catalog table. The roll-up always
// prints both percentages, and prints the Mokka-unlocked split so the headline
// cannot be read as more than it is.
func (r Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder

	b.WriteString("# AICR check coverage under Mokka\n\n")
	fmt.Fprintf(&b, "Generated %s. Provenance: `%s`. Cluster: `%s`. Profile: `%s`.\n\n",
		r.GeneratedAt, r.Provenance, orNone(r.Cluster), orNone(r.Profile))
	fmt.Fprintf(&b, "Catalog source: %s\n\n", orNone(r.CatalogSrc))

	b.WriteString("## Roll-up\n\n")
	fmt.Fprintf(&b, "- Checks total: %d (A: %d, B: %d, C: %d, G: %d)\n",
		r.Summary.Total, r.Summary.ByBucket[BucketA], r.Summary.ByBucket[BucketB],
		r.Summary.ByBucket[BucketC], r.Summary.ByBucket[BucketG])
	fmt.Fprintf(&b, "- Meaningful pre-silicon TODAY: %.1f%% (A / total)\n", r.Summary.MeaningfulTodayPct)
	fmt.Fprintf(&b, "- Reachable once tracked gaps close: %.1f%% ((A + G) / total). Roadmap, not a current claim.\n",
		r.Summary.ReachablePct)
	fmt.Fprintf(&b, "- Of the %d bucket-A checks, %d depend on the GPU stack (unlocked by Mokka) and %d are GPU-independent (any cluster runs them).\n",
		r.Summary.ByBucket[BucketA], r.Summary.MokkaUnlockedA, r.Summary.GPUIndependentA)

	// Bucket assignment is analysis over the whole catalog; execution is what
	// this run actually observed. Reporting only the first would imply evidence
	// the run does not have.
	executed := r.Summary.ByOutcom[OutcomePass] + r.Summary.ByOutcom[OutcomeFail] + r.Summary.ByOutcom[OutcomeSkip]
	fmt.Fprintf(&b, "- Executed in this run: %d of %d (%d pass, %d fail, %d skip). **%d were not run** and carry no evidence either way.\n",
		executed, r.Summary.Total,
		r.Summary.ByOutcom[OutcomePass], r.Summary.ByOutcom[OutcomeFail],
		r.Summary.ByOutcom[OutcomeSkip], r.Summary.ByOutcom[OutcomeNotRun])

	if r.Summary.ByBucket[BucketB] == 0 {
		b.WriteString("- Bucket B is empty. That is a real result, not an oversight: AICR's checks are written as integration assertions rather than value assertions, so none of them is green purely because the mock answered. The corresponding limitation is that where Mokka's values are synthetic, a check validates the path and stays silent on the values.\n")
	}

	if r.Summary.SuspectCount > 0 {
		fmt.Fprintf(&b, "- **%d suspect result(s)**: a hardware-dependent check reported green under simulation. Review before quoting.\n",
			r.Summary.SuspectCount)
	}
	if len(r.Uncatalogued) > 0 {
		fmt.Fprintf(&b, "- **Catalog drift**: the run reported %d check(s) the catalog does not describe: %s\n",
			len(r.Uncatalogued), strings.Join(r.Uncatalogued, ", "))
	}

	b.WriteString("\n## Catalog\n\n")
	b.WriteString("| # | Check | Area | Phase | Bucket | GPU-dep | Outcome | Provenance | Gap issue |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for i, rec := range r.Records {
		suspect := ""
		if rec.Suspect {
			suspect = " **(suspect)**"
		}
		fmt.Fprintf(&b, "| %d | `%s` | %s | %s | %s | %s | %s%s | %s | %s |\n",
			i+1, rec.Name, orNone(rec.Area), orNone(rec.Phase), rec.Bucket,
			yesNo(rec.GPUDependent), rec.Outcome, suspect, rec.Provenance, orNone(rec.GapIssue))
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
