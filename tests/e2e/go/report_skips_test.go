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

	var lines []string
	for _, spec := range report.SpecReports {
		if spec.State != types.SpecStateSkipped {
			continue
		}
		reason := strings.TrimSpace(spec.Failure.Message)
		if reason == "" {
			reason = "(filtered: did not match label filter)"
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
