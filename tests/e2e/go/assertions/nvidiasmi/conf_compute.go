// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strings"
)

// ConfComputeMemoryProblems checks every GPU's <cc_protected_memory_usage>
// block reports zero across total, used and free.
//
// Zero is the point of the check rather than a weak expectation. Every row read
// N/A while the two NVML getters behind them were generated stubs (#711), which
// says the driver answered nothing where real hardware answers none: all seven
// hardware captures report 0 MiB here, including the A100, L40S and T4 boards
// that cannot do Confidential Compute at all. The expectation is therefore not
// derived from the profile — nothing partitions protected memory until CC mode
// is on, which no profile models.
func ConfComputeMemoryProblems(out string) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		name := gpu.label(i) + " cc_protected_memory_usage"
		rows := []struct {
			element string
			got     reading
		}{
			{"total", gpu.CCProtectedMemoryUsage.Total},
			{"used", gpu.CCProtectedMemoryUsage.Used},
			{"free", gpu.CCProtectedMemoryUsage.Free},
		}
		for _, row := range rows {
			problems = append(problems, ccProtectedRowProblems(name+"/"+row.element, row.got)...)
		}
	}
	return problems
}

// ccProtectedRowProblems compares one row, which must read zero MiB. An N/A
// body is reported as the missing getter it is rather than as a wrong value,
// matching intReadingProblems, and a non-zero value fails because no profile
// switches CC mode on.
func ccProtectedRowProblems(label string, got reading) []string {
	value, ok := got.intValue()
	switch {
	case !ok:
		return []string{fmt.Sprintf(
			"%s = %q, want 0 MiB; a non-numeric reading means the NVML getter is missing or unimplemented",
			label, strings.TrimSpace(string(got)))}
	case value != 0:
		return []string{fmt.Sprintf(
			"%s = %d MiB, want 0 MiB: no profile switches Confidential Compute mode on",
			label, value)}
	}
	return nil
}
