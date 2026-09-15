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
		{"-gr", requestedOut, requestedExit, powerProfileNoneRequested},
		{"-ge", enforcedOut, enforcedExit, powerProfileNoneEnforced},
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

// Confirmation lines nvidia-smi prints once per GPU for an accepted write.
const (
	powerProfileSetConfirmation   = "Successfully set the requested profiles."
	powerProfileClearConfirmation = "Successfully cleared the requested profiles."
)

// PowerProfileRoundTripProblems checks a set, a clear and a read carried out in
// one `nvidia-smi power-profiles` invocation, e.g. `-sr 6,13 -cr 6 -ge`.
//
// `-gr` cannot stand in for `-ge` here: nvidia-smi evaluates the requested
// query before the write and the enforced query after it, so within one
// invocation only the enforced query observes what was just set.
// PowerProfileRequestedProblems covers the read landing in a later invocation.
//
// wantEnforced is what must survive the clear. Checking the surviving profile
// rather than only the confirmation lines is the point: a clear that wiped
// everything, or one that did nothing, both still print success.
func PowerProfileRoundTripProblems(out string, exitCode, wantGPUs int, wantEnforced []int) []string {
	if exitCode != 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles set/clear round trip exited %d, want 0: %s",
			exitCode, strings.TrimSpace(out))}
	}

	var problems []string
	for _, want := range []string{powerProfileSetConfirmation, powerProfileClearConfirmation} {
		if got := strings.Count(out, want); got != wantGPUs {
			problems = append(problems, fmt.Sprintf(
				"nvidia-smi power-profiles printed %q %d time(s), want %d (one per GPU): %s",
				want, got, wantGPUs, strings.TrimSpace(out)))
		}
	}

	// A clear that removed everything, not just the profile it was given,
	// prints the empty sentence instead of a list. Naming that outcome keeps
	// it from surfacing as a GPU count mismatch below.
	if len(wantEnforced) > 0 && strings.Contains(out, powerProfileNoneEnforced) {
		problems = append(problems, fmt.Sprintf(
			"nvidia-smi power-profiles enforced nothing after the clear, want %v: "+
				"the clear removed more than the profile it was given: %s",
			wantEnforced, strings.TrimSpace(out)))
		return problems
	}

	problems = append(problems,
		profileListProblems(out, wantGPUs, wantEnforced, "enforced", "after the clear")...)
	return problems
}

// How nvidia-smi renders an empty set for each query.
const (
	powerProfileNoneEnforced  = "No profiles are currently engaged."
	powerProfileNoneRequested = "No profiles are currently requested."
)

// PowerProfileRequestedProblems checks a `-gr` read against what an earlier
// `-sr` asked for, where the two ran as separate nvidia-smi invocations.
//
// The separation is the assertion. A profile request is driver state on real
// hardware, so a second nvidia-smi observes what the first one set; while the
// mock held the request in the writing process's memory this read reported
// nothing at all, and no single-invocation check could tell the difference.
func PowerProfileRequestedProblems(out string, exitCode, wantGPUs int, wantRequested []int) []string {
	if exitCode != 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -gr exited %d, want 0: %s",
			exitCode, strings.TrimSpace(out))}
	}
	// Naming this outcome keeps the write not surviving the process from
	// surfacing as a GPU count mismatch below.
	if len(wantRequested) > 0 && strings.Contains(out, powerProfileNoneRequested) {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -gr reported nothing requested, want %v: "+
				"the request did not outlive the invocation that set it: %s",
			wantRequested, strings.TrimSpace(out))}
	}
	return profileListProblems(out, wantGPUs, wantRequested, "requested", "in a later invocation")
}

// profileListProblems compares the per-GPU lists nvidia-smi rendered against
// want. what names the query for the failure message.
func profileListProblems(out string, wantGPUs int, want []int, what, when string) []string {
	blocks := powerProfileBlocks(out)
	if len(blocks) != wantGPUs {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles reported %s profiles for %d GPU(s), want %d: %s",
			what, len(blocks), wantGPUs, strings.TrimSpace(out))}
	}
	var problems []string
	for i, got := range blocks {
		if !equalInts(got, want) {
			problems = append(problems, fmt.Sprintf(
				"GPU %d %s %v %s, want %v", i, what, got, when, want))
		}
	}
	return problems
}

// PowerProfileArbitrationProblems checks that requesting two profiles that
// exclude each other leaves only the higher-priority one enforced, via
// `-sr <winner>,<loser> -ge`.
//
// This is the one assertion that separates the requested set from the enforced
// set through the CLI: a mock that echoed the request straight back would list
// both and pass every other check here.
func PowerProfileArbitrationProblems(out string, exitCode, wantGPUs, wantWinner, loser int) []string {
	if exitCode != 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -sr %d,%d -ge exited %d, want 0: %s",
			wantWinner, loser, exitCode, strings.TrimSpace(out))}
	}

	var problems []string
	if got := strings.Count(out, powerProfileSetConfirmation); got != wantGPUs {
		problems = append(problems, fmt.Sprintf(
			"nvidia-smi power-profiles printed %q %d time(s), want %d (one per GPU): %s",
			powerProfileSetConfirmation, got, wantGPUs, strings.TrimSpace(out)))
	}

	problems = append(problems, profileListProblems(out, wantGPUs, []int{wantWinner}, "enforced",
		fmt.Sprintf("after requesting conflicting profiles %d and %d", wantWinner, loser))...)
	return problems
}

// PowerProfileSetRejectedProblems checks a profile the board never advertised is
// refused. nvidia-smi validates the id against the advertised list before it
// calls NVML, so this also confirms the list the board reports is the list
// nvidia-smi believes.
func PowerProfileSetRejectedProblems(out string, exitCode, badID int) []string {
	if exitCode == 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -sr %d succeeded on a profile the board does not "+
				"advertise, want it refused: %s", badID, strings.TrimSpace(out))}
	}
	want := fmt.Sprintf("Power Profile: %d is not a supported profile number.", badID)
	if !strings.Contains(out, want) {
		return []string{fmt.Sprintf(
			"nvidia-smi power-profiles -sr %d exited %d but did not report %q: %s",
			badID, exitCode, want, strings.TrimSpace(out))}
	}
	return nil
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
