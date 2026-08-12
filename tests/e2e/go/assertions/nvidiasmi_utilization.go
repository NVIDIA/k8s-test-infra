// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file carries no build tag on purpose — same rationale as
// nvidiasmi_temperature.go. DiffJpgOfaUtilizationQuery is pure string checking
// so it unit-tests without a cluster; the kubectl exec wrapper lives in
// nvidiasmi.go under //go:build e2e.

var utilizationRowRE = regexp.MustCompile(`(?m)^\s*(JPEG|OFA)\s*:\s*(.+?)\s*$`)

// DiffJpgOfaUtilizationQuery checks the JPEG and OFA rows of
// `nvidia-smi -q -d UTILIZATION` against the configured utilization.jpeg and
// utilization.ofa percentages. Every GPU in the output is checked, so a getter
// that answers for only one device is caught.
//
// An "N/A" reading is the defect this exists to catch (#637): both values were
// parsed from the config and then dropped, because the NVML entry points were
// generated stubs returning NOT_SUPPORTED.
func DiffJpgOfaUtilizationQuery(out string, wantJPEG, wantOFA int) []string {
	want := map[string]int{"JPEG": wantJPEG, "OFA": wantOFA}
	seen := map[string]int{}

	var problems []string
	for _, m := range utilizationRowRE.FindAllStringSubmatch(out, -1) {
		label, raw := m[1], m[2]
		seen[label]++
		pct, ok := parseUtilizationPercent(raw)
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s = %q, want %d %%; a non-numeric reading means the NVML getter is missing or unimplemented",
				label, raw, want[label]))
			continue
		}
		if pct != want[label] {
			problems = append(problems, fmt.Sprintf("%s = %d %%, want %d %%", label, pct, want[label]))
		}
	}

	for _, label := range []string{"JPEG", "OFA"} {
		if seen[label] == 0 {
			problems = append(problems, fmt.Sprintf("missing %q row", label))
		}
	}
	return problems
}

func parseUtilizationPercent(raw string) (int, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	pct, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false
	}
	return pct, true
}
