// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// `nvidia-smi power-profiles` reads the two workload power profile getters.
// While they were generated stubs the whole subcommand answered "Workload Power
// Profiles feature is not supported on this device" on every profile, Blackwell
// included, so a consumer could not discover a single profile the board offers.

// powerProfileLine matches one profile in `power-profiles -l` output, e.g.
// "6. LLM Inference". -ld indents the metadata beneath it, so the anchor keeps
// a "Priority:" or a conflict entry from being read as a profile of its own.
var powerProfileLine = regexp.MustCompile(`^(\d+)\.\s+(\S.*)$`)

// notSupportedMarkers are how nvidia-smi reports each way the subcommand can be
// declined. They are distinct outcomes: a board that does not model the feature
// is told the device does not support it, while a pre-570 driver does not export
// the symbol at all.
var notSupportedMarkers = []string{
	"not supported on this device",
	"Function Not Found",
}

// PowerProfileListProblems checks `nvidia-smi power-profiles -l` listed exactly
// wantIDs on every GPU. The output repeats one block per GPU with no header
// between them, so the ids are collected per block of ascending runs.
//
// Exact equality both ways is the point. A missing id is the stub behaviour
// returning, and an extra one is the uninitialised-tail bug: the bridge writes
// a 255-entry array into a buffer nvidia-smi does not clear, so leaving the tail
// untouched renders profiles the board never advertised.
func PowerProfileListProblems(out string, exitCode, wantGPUs int, wantIDs []int) []string {
	if exitCode != 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -l exited %d, want 0: %s", exitCode, strings.TrimSpace(out))}
	}

	blocks := powerProfileBlocks(out)
	if len(blocks) != wantGPUs {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -l listed profiles for %d GPU(s), want %d: %s",
			len(blocks), wantGPUs, strings.TrimSpace(out))}
	}

	var problems []string
	for i, got := range blocks {
		if !equalInts(got, wantIDs) {
			problems = append(problems, fmt.Sprintf(
				"GPU %d listed power profiles %v, want %v", i, got, wantIDs))
		}
	}
	return problems
}

// PowerProfileUnsupportedProblems checks the subcommand is declined the way a
// board without the feature declines it. This is the negative control: without
// it, a regression that reported an empty profile set on every board would pass
// the positive check on gb200 alone.
func PowerProfileUnsupportedProblems(out string, exitCode int) []string {
	if exitCode == 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -l succeeded on a profile that declares no workload "+
				"power profiles, want it declined: %s", strings.TrimSpace(out))}
	}
	for _, marker := range notSupportedMarkers {
		if strings.Contains(out, marker) {
			return nil
		}
	}
	return []string{fmt.Sprintf(
		"nvidia-smi power-profiles -l exited %d but said none of %v: %s",
		exitCode, notSupportedMarkers, strings.TrimSpace(out))}
}

// PowerProfileCurrentProblems checks the requested and enforced queries against
// a board that has asked for nothing, which is what every capture under
// testdata/hardware reports. nvidia-smi renders that as a sentence rather than
// an empty list, so an empty answer is distinguishable from a failed one.
func PowerProfileCurrentProblems(requestedOut string, requestedExit int, enforcedOut string, enforcedExit int) []string {
	var problems []string
	for _, tc := range []struct {
		flag string
		out  string
		exit int
		want string
	}{
		{"-gr", requestedOut, requestedExit, "No profiles are currently requested."},
		{"-ge", enforcedOut, enforcedExit, "No profiles are currently engaged."},
	} {
		switch {
		case tc.exit != 0:
			problems = append(problems, fmt.Sprintf(
				"nvidia-smi power-profiles %s exited %d, want 0: %s",
				tc.flag, tc.exit, strings.TrimSpace(tc.out)))
		case !strings.Contains(tc.out, tc.want):
			problems = append(problems, fmt.Sprintf(
				"nvidia-smi power-profiles %s did not report %q: %s",
				tc.flag, tc.want, strings.TrimSpace(tc.out)))
		}
	}
	return problems
}

// powerProfileBlocks splits the per-GPU repetitions apart. nvidia-smi prints
// each GPU's list back to back with nothing in between, so a block ends where
// the id stops increasing.
func powerProfileBlocks(out string) [][]int {
	var blocks [][]int
	var current []int
	for line := range strings.SplitSeq(out, "\n") {
		m := powerProfileLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if len(current) > 0 && id <= current[len(current)-1] {
			blocks = append(blocks, current)
			current = nil
		}
		current = append(current, id)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	return blocks
}

// equalInts compares two id lists irrespective of order.
func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]int(nil), got...)
	w := append([]int(nil), want...)
	sort.Ints(g)
	sort.Ints(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}
