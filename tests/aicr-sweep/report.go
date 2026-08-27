// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// pct renders a share as a percentage string. A zero denominator renders "n/a"
// rather than 0%, because 0% reads as a measured result and n/a does not.
func pct(num, den int) string {
	if den == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(num)/float64(den))
}

// RenderJSON emits the machine-readable sweep record.
func RenderJSON(cells []CellResult, roll Rollup) ([]byte, error) {
	doc := struct {
		Rollup Rollup       `json:"rollup"`
		Cells  []CellResult `json:"cells"`
	}{Rollup: roll, Cells: cells}
	return json.MarshalIndent(doc, "", "  ")
}

// RenderMarkdown emits the human-readable coverage read.
//
// Two numbers always appear together: A/total is what carries real pre-silicon
// signal today, and (A+G)/total is what becomes reachable once tracked gaps
// close. The second is never printed without the first, because presenting the
// roadmap number alone is the specific overclaim this sweep exists to avoid.
//
//nolint:cyclop // existing complexity; refactor deferred
func RenderMarkdown(cells []CellResult, roll Rollup) string {
	var b strings.Builder

	b.WriteString("# AICR check coverage under Mokka\n\n")
	b.WriteString("All evidence below is simulation provenance (`sim`). None of it was run on silicon.\n\n")

	b.WriteString("## Roll-up\n\n")
	fmt.Fprintf(&b, "- Check results total: %d (checks x cells)\n", roll.Total)
	fmt.Fprintf(&b, "- A meaningful: %d  ·  B trivial: %d  ·  C hardware-dependent: %d  ·  G closable gap: %d\n",
		roll.A, roll.B, roll.C, roll.G)
	fmt.Fprintf(&b, "- **Meaningful pre-silicon today: %s** (A / total)\n", pct(roll.A, roll.Total))
	fmt.Fprintf(&b, "- **Reachable once tracked gaps close: %s** ((A + G) / total). Roadmap, not a current claim.\n",
		pct(roll.A+roll.G, roll.Total))
	fmt.Fprintf(&b, "- Of the A set, %d are GPU-dependent (%s of A). The remainder run on any cluster, so Mokka is not what unlocks them.\n",
		roll.AGPUDependent, pct(roll.AGPUDependent, roll.A))
	fmt.Fprintf(&b, "- Never reached a verdict (blocked or not-run): %d (%s)\n", roll.Blocked, pct(roll.Blocked, roll.Total))
	if roll.Suspect > 0 {
		fmt.Fprintf(&b, "- **Suspect: %d** hardware-dependent checks reported a pass without hardware. See the cell tables.\n", roll.Suspect)
	}

	if len(roll.ByCause) > 0 {
		b.WriteString("\n### Why cells did not reach a verdict\n\n")
		b.WriteString("| Cause | Meaning | Count |\n|---|---|---|\n")
		meanings := map[Cause]string{
			CauseK:      "kind or single-host artifact. NOT a Mokka finding.",
			CauseU:      "upstream dependency has not released what is needed. NOT a Mokka gap.",
			CauseX:      "AICR catalog has no recipe for the combination. NOT a Mokka gap.",
			CauseBudget: "host memory budget exhausted; cell not attempted.",
		}
		causes := make([]Cause, 0, len(roll.ByCause))
		for c := range roll.ByCause {
			causes = append(causes, c)
		}
		sort.Slice(causes, func(i, j int) bool { return causes[i] < causes[j] })
		for _, c := range causes {
			fmt.Fprintf(&b, "| %s | %s | %d |\n", c, meanings[c], roll.ByCause[c])
		}
	}

	b.WriteString("\n## Cells\n\n")
	for _, cell := range cells {
		fmt.Fprintf(&b, "### `%s`\n\n", cell.Cell.ID)
		fmt.Fprintf(&b, "Recipe: service=%s accelerator=%s intent=%s · shape=%s workers=%d · status=%s\n\n",
			cell.Cell.Recipe.Service, cell.Cell.Recipe.Accelerator, cell.Cell.Recipe.Intent,
			cell.Cell.Shape, cell.Cell.Workers, cell.Status)
		if cell.BlockedWhy != "" {
			fmt.Fprintf(&b, "**Blocked (%s):** %s\n\n", cell.Cause, cell.BlockedWhy)
		}
		if len(cell.Cell.Axes) > 0 {
			keys := make([]string, 0, len(cell.Cell.Axes))
			for k := range cell.Cell.Axes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%s", k, cell.Cell.Axes[k]))
			}
			fmt.Fprintf(&b, "Axes off base: %s\n\n", strings.Join(parts, ", "))
		}

		b.WriteString("| Check | Phase | Bucket | GPU-dep | Outcome | Cause | Note |\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		for _, c := range cell.Checks {
			note := c.Note
			if c.Suspect {
				note = "SUSPECT: " + note
			}
			gpu := "no"
			if c.GPUDep {
				gpu = "yes"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
				c.Check, c.Phase, c.Bucket, gpu, c.Outcome, c.Cause, note)
		}
		if len(cell.Drift) > 0 {
			fmt.Fprintf(&b, "\n**Catalog drift:** the run reported checks absent from the catalog: %s\n",
				strings.Join(cell.Drift, ", "))
		}
		b.WriteString("\n")
	}

	return b.String()
}
