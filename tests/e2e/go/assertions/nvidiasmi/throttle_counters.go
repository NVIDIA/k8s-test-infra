// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strings"
)

// The clocks-event counter checks, kept out of checks.go because they compare a
// block of related readings per GPU and because the injection check compares
// two different expectations within one document.

// ThrottleCounterBlock is the <clocks_event_reasons_counters> block as
// microsecond totals — the unit nvidia-smi prints and a real tray reports. Zero
// is the reading of a GPU that has never been throttled by that cause, and is
// asserted as such: before #678 every element read N/A, which says the driver
// answered nothing, and a check that accepted both could not tell them apart.
type ThrottleCounterBlock struct {
	SWPowerCapUS        int
	SyncBoostUS         int
	SWThermalSlowdownUS int
	HWThermalSlowdownUS int
	HWPowerBrakeUS      int
}

// UnthrottledCounters is the block a GPU that has never been throttled reports:
// five zeros. It is the healthy baseline every profile must produce.
func UnthrottledCounters() ThrottleCounterBlock { return ThrottleCounterBlock{} }

// ThrottleCounterProblems checks every GPU's counter block against want.
func ThrottleCounterProblems(out string, want ThrottleCounterBlock) []string {
	return throttleCounterProblems(out, func(int) ThrottleCounterBlock { return want })
}

// ThrottleCounterProblemsAt checks the GPU at index against want and every other
// GPU against others. Accrued throttle time is per device, so a counter that
// leaks onto the node's other GPUs — or one seeded on the wrong device — is a
// defect this reports rather than one it passes over.
func ThrottleCounterProblemsAt(out string, index int, want, others ThrottleCounterBlock) []string {
	return throttleCounterProblems(out, func(i int) ThrottleCounterBlock {
		if i == index {
			return want
		}
		return others
	})
}

func throttleCounterProblems(out string, want func(int) ThrottleCounterBlock) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		problems = append(problems,
			throttleCounterBlockProblems(gpu.label(i), gpu.ClocksEventCounters, want(i))...)
	}
	return problems
}

// throttleCounterBlockProblems reports one GPU's differences, one element per
// problem, so a failure names the counter that regressed instead of dumping the
// block. N/A is called out separately from a wrong number because the two have
// different causes: an unanswered field id versus a miscounted duration.
func throttleCounterBlockProblems(name string, got eventCounters, want ThrottleCounterBlock) []string {
	var problems []string
	for _, element := range []struct {
		name string
		got  reading
		want int
	}{
		{"sw_power_cap", got.SWPowerCap, want.SWPowerCapUS},
		{"sync_boost", got.SyncBoost, want.SyncBoostUS},
		{"sw_therm_slowdown", got.SWThermalSlowdown, want.SWThermalSlowdownUS},
		{"hw_therm_slowdown", got.HWThermalSlowdown, want.HWThermalSlowdownUS},
		{"hw_power_brake", got.HWPowerBrake, want.HWPowerBrakeUS},
	} {
		body := strings.TrimSpace(string(element.got))
		value, numeric := element.got.intValue()
		switch {
		case !element.got.present():
			problems = append(problems, fmt.Sprintf(
				"%s emits no %s counter, want %d us; the driver may have renamed it",
				name, element.name, element.want))
		case !numeric:
			problems = append(problems, fmt.Sprintf(
				"%s %s counter = %q, want %d us: the driver declined the query rather than answering it",
				name, element.name, body, element.want))
		case value != element.want:
			problems = append(problems, fmt.Sprintf("%s %s counter = %d us, want %d us",
				name, element.name, value, element.want))
		}
	}
	return problems
}
