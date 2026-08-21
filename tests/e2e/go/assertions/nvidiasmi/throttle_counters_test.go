// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// powerCapped is the block of a GPU that has spent 39595 us under its software
// power cap and has never been throttled for any other reason — the reading the
// GB300 tray in #678 produced, kept verbatim so the fixture and the expectation
// come from the same place.
func powerCapped() ThrottleCounterBlock {
	want := UnthrottledCounters()
	want.SWPowerCapUS = 39595
	return want
}

// qx-gb200-healthy.xml was captured before #678, so it is the defect itself:
// nvidia-smi rendered all five counters as N/A because the mock left the field
// ids behind them unanswered, so a consumer could not tell a GPU that had never
// throttled from one whose history the driver would not report.
func TestThrottleCounterProblems_RejectsUnansweredCounters(t *testing.T) {
	problems := ThrottleCounterProblems(loadFixture(t, "qx-gb200-healthy.xml"), UnthrottledCounters())
	require.Len(t, problems, 10, "five unanswered counters on each of the two GPUs")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, `sw_power_cap counter = "N/A", want 0 us`)
	assert.Contains(t, joined, "declined the query rather than answering it",
		"N/A must be reported as an unanswered query, not as a wrong number")
}

// The healthy direction: five zeros, which is a real answer where N/A is not.
func TestThrottleCounterProblems_AcceptsNeverThrottledGPU(t *testing.T) {
	out := loadFixture(t, "qx-gb200-throttle-counters.xml")
	problems := ThrottleCounterProblemsAt(out, 0, powerCapped(), UnthrottledCounters())
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The assertion that gives the feature its value: a seeded cause reports its own
// total and the other four stay at zero. A single counter answered for every
// field id — the cheapest way to stop the N/A — would satisfy a check that only
// looked at the seeded one.
func TestThrottleCounterProblems_ReportsOnlyTheSeededCause(t *testing.T) {
	problems := ThrottleCounterProblems(loadFixture(t, "qx-gb200-throttle-counters.xml"),
		UnthrottledCounters())
	require.Len(t, problems, 1, "only GPU 0's power-capping total may differ")
	assert.Contains(t, problems[0], "sw_power_cap counter = 39595 us, want 0 us")
}

// Accrued time is per device, so a total reported on the wrong GPU is a defect
// and not a pass: the same document must fail when the history is expected
// elsewhere.
func TestThrottleCounterProblems_RejectsHistoryOnTheWrongGPU(t *testing.T) {
	problems := ThrottleCounterProblemsAt(loadFixture(t, "qx-gb200-throttle-counters.xml"),
		1, powerCapped(), UnthrottledCounters())
	require.Len(t, problems, 2, "both GPUs report the opposite of what was expected")
	assert.Contains(t, strings.Join(problems, "; "), "sw_power_cap counter = 0 us, want 39595 us",
		"the GPU expected to carry the history reports none")
}

// An element the driver renamed is reported as absent rather than compared as an
// empty body, matching how the rest of the package reads the document.
func TestThrottleCounterProblems_ReportsMissingElement(t *testing.T) {
	out := strings.ReplaceAll(loadFixture(t, "qx-gb200-throttle-counters.xml"),
		"<clocks_event_reasons_counters_sync_boost>0 us</clocks_event_reasons_counters_sync_boost>", "")
	problems := ThrottleCounterProblems(out, powerCapped())
	assert.Contains(t, strings.Join(problems, "; "), "emits no sync_boost counter")
}

func TestThrottleCounterProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := ThrottleCounterProblems("not xml", UnthrottledCounters())
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}
