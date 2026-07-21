//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// ReportAfterSuite prints a single, easy-to-find summary of every skipped spec
// and its reason. Ginkgo scatters per-spec `S` markers through the verbose
// multi-profile log; this consolidates them so it is obvious at a glance what
// was skipped and why. It is purely informational and never fails the suite.
var _ = ReportAfterSuite("skipped specs summary", func(report Report) {
	// ReportAfterSuite runs once, but guard anyway so parallel runners never
	// emit the block twice.
	if GinkgoParallelProcess() != 1 {
		return
	}

	// A skipped spec with an empty Failure.Message was not skipped via a
	// programmatic Skip("..."); it could be a label-filter/focus/skip-string
	// exclusion, a suite-wide skip, an interrupt, or a deadline. We cannot tell
	// these apart per spec, so fall back to a cause-neutral marker that only
	// names the filters actually configured for this run.
	fallback := emptySkipReason(report.SuiteConfig)

	var lines []string
	for _, spec := range report.SpecReports {
		if spec.State != types.SpecStateSkipped {
			continue
		}
		reason := strings.TrimSpace(spec.Failure.Message)
		if reason == "" {
			reason = fallback
		}
		lines = append(lines, fmt.Sprintf("- %s\n    reason: %s", spec.FullText(), reason))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n========== SKIPPED SPECS (%d) ==========\n", len(lines))
	if len(lines) == 0 {
		b.WriteString("No specs skipped.\n")
	} else {
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("=======================================\n")

	fmt.Fprint(GinkgoWriter, b.String())
})

// emptySkipReason returns a cause-neutral marker for skipped specs that carry no
// Failure.Message. Such skips have no per-spec reason we can recover, so we only
// hint at the filters actually configured for this run rather than asserting a
// single cause.
func emptySkipReason(cfg types.SuiteConfig) string {
	var filters []string
	if strings.TrimSpace(cfg.LabelFilter) != "" {
		filters = append(filters, fmt.Sprintf("label-filter %q", cfg.LabelFilter))
	}
	if len(cfg.FocusStrings) > 0 {
		filters = append(filters, fmt.Sprintf("focus %v", cfg.FocusStrings))
	}
	if len(cfg.SkipStrings) > 0 {
		filters = append(filters, fmt.Sprintf("skip %v", cfg.SkipStrings))
	}
	if len(filters) == 0 {
		return "(no skip reason reported)"
	}
	return fmt.Sprintf("(no skip reason reported; possibly excluded by %s)", strings.Join(filters, ", "))
}
