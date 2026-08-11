// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file carries no build tag on purpose — same rationale as gfd_labels.go.
// DiffTemperatureQuery is pure string checking so it unit-tests without a
// cluster; the kubectl exec wrapper lives in nvidiasmi.go under //go:build e2e.

var tempQueryRowRE = regexp.MustCompile(`(?m)^\s*(GPU .+? Temp)\s*:\s*(-?\d+)\s*C\s*$`)

// DiffTemperatureQuery checks nvidia-smi -q -d TEMPERATURE output for the
// architecture-correct threshold presentation:
//
//   - pre-Ada (reportsTLimit=false): absolute "GPU Shutdown/Slowdown/Max
//     Operating Temp" rows equal to the profile thresholds, and no T.Limit row
//     carrying a value.
//   - Ada+ (reportsTLimit=true): the "T.Limit" row labels are present.
//
// Only rows with a numeric reading are considered: nvidia-smi still prints a
// "GPU T.Limit Temp : N/A" line on pre-Ada, and an N/A row is exactly what a
// real unsupported query looks like. A T.Limit row with a NUMBER on pre-Ada is
// the defect. Absolute threshold rows must never be negative or ordered with
// shutdown below slowdown — the impossible rendering the gate fixes.
func DiffTemperatureQuery(out string, reportsTLimit bool, shutdownC, slowdownC, maxOperatingC int) []string {
	rows := parseTemperatureQueryRows(out)
	var problems []string

	if reportsTLimit {
		for _, label := range []string{
			"GPU Shutdown T.Limit Temp",
			"GPU Slowdown T.Limit Temp",
			"GPU Max Operating T.Limit Temp",
		} {
			if _, ok := rows[label]; !ok {
				problems = append(problems, fmt.Sprintf("missing %q row", label))
			}
		}
		for _, label := range []string{
			"GPU Shutdown Temp",
			"GPU Slowdown Temp",
			"GPU Max Operating Temp",
		} {
			if _, ok := rows[label]; ok {
				problems = append(problems, fmt.Sprintf("unexpected absolute %q row on Ada+ profile", label))
			}
		}
		return problems
	}

	want := map[string]int{
		"GPU Shutdown Temp":      shutdownC,
		"GPU Slowdown Temp":      slowdownC,
		"GPU Max Operating Temp": maxOperatingC,
	}
	for label, wantC := range want {
		got, ok := rows[label]
		if !ok {
			problems = append(problems, fmt.Sprintf("missing absolute %q row", label))
			continue
		}
		if got != wantC {
			problems = append(problems, fmt.Sprintf("%s = %d C, want %d C", label, got, wantC))
		}
	}
	for _, label := range []string{
		"GPU Shutdown T.Limit Temp",
		"GPU Slowdown T.Limit Temp",
		"GPU Max Operating T.Limit Temp",
		"GPU T.Limit Temp",
	} {
		if _, ok := rows[label]; ok {
			problems = append(problems, fmt.Sprintf("unexpected T.Limit %q row on pre-Ada profile", label))
		}
	}

	shutdown, hasShutdown := rows["GPU Shutdown Temp"]
	slowdown, hasSlowdown := rows["GPU Slowdown Temp"]
	if hasShutdown && shutdown < 0 {
		problems = append(problems, fmt.Sprintf("GPU Shutdown Temp is negative (%d C)", shutdown))
	}
	if hasSlowdown && slowdown < 0 {
		problems = append(problems, fmt.Sprintf("GPU Slowdown Temp is negative (%d C)", slowdown))
	}
	if hasShutdown && hasSlowdown && shutdown < slowdown {
		problems = append(problems, fmt.Sprintf(
			"GPU Shutdown Temp (%d C) is below GPU Slowdown Temp (%d C)", shutdown, slowdown))
	}
	return problems
}

func parseTemperatureQueryRows(out string) map[string]int {
	rows := make(map[string]int)
	for _, m := range tempQueryRowRE.FindAllStringSubmatch(out, -1) {
		label := strings.TrimSpace(m[1])
		val, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		rows[label] = val
	}
	return rows
}
