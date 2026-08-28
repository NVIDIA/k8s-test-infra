// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boostGPU renders one <gpu> element carrying the two graphics_clock bodies the
// check compares, so a test can inject a value, "N/A" or nothing at all. The
// element name repeats under both parents, which is the confusion the check has
// to be proof against.
func boostGPU(id, maxClock, boostClock string) string {
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA GB300</product_name>
		<clocks>
			<graphics_clock>345 MHz</graphics_clock>
		</clocks>
		<max_clocks>
			<graphics_clock>` + maxClock + `</graphics_clock>
		</max_clocks>
		<max_customer_boost_clocks>
			<graphics_clock>` + boostClock + `</graphics_clock>
		</max_customer_boost_clocks>
	</gpu>`
}

func TestMaxCustomerBoostClockProblems_AcceptsConfiguredCeiling(t *testing.T) {
	out := xmlDocument(
		boostGPU("0000:07:00.0", "2070 MHz", "2070 MHz"),
		boostGPU("0000:0F:00.0", "2070 MHz", "2070 MHz"),
	)

	problems := MaxCustomerBoostClockProblems(out, 2070)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The defect this exists to catch (#712): N/A says the driver cannot report an
// OEM boost ceiling, where every real board reports one. The mock fixtures were
// captured while both NVML getters behind the row were generated stubs.
func TestMaxCustomerBoostClockProblems_RejectsNotAvailableReading(t *testing.T) {
	problems := MaxCustomerBoostClockProblems(loadFixture(t, "qx-gb200-healthy.xml"), 2062)

	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, "N/A")
	assert.Contains(t, joined, "getter is missing")
}

func TestMaxCustomerBoostClockProblems_RejectsWrongCeiling(t *testing.T) {
	out := xmlDocument(boostGPU("0000:07:00.0", "2200 MHz", "2200 MHz"))

	problems := MaxCustomerBoostClockProblems(out, 2070)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "\n"), "2200")
}

// The invariant that lets the mock resolve the row from clocks.graphics_max: a
// profile whose two rows disagree has to justify itself, because no captured
// board does.
func TestMaxCustomerBoostClockProblems_RejectsCeilingBelowBoostMax(t *testing.T) {
	out := xmlDocument(boostGPU("0000:07:00.0", "2070 MHz", "2062 MHz"))

	problems := MaxCustomerBoostClockProblems(out, 2062)
	require.NotEmpty(t, problems, "2062 matches the wanted value but contradicts max_clocks")
	assert.Contains(t, strings.Join(problems, "\n"), "max_clocks")
}

// A missing boost maximum leaves nothing to compare against, and that is a
// rename rather than an agreement.
func TestMaxCustomerBoostClockProblems_ReportsMissingBoostMax(t *testing.T) {
	out := xmlDocument(boostGPU("0000:07:00.0", "", "2070 MHz"))

	problems := MaxCustomerBoostClockProblems(out, 2070)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "max_clocks/graphics_clock")
}

// The row sits beside <clocks> and <max_clocks>, which both repeat
// graphics_clock, so a check reading the wrong block would pass against the
// current clock.
func TestMaxCustomerBoostClockProblems_DoesNotReadTheCurrentClockBlock(t *testing.T) {
	out := xmlDocument(boostGPU("0000:07:00.0", "2070 MHz", "N/A"))

	problems := MaxCustomerBoostClockProblems(out, 2070)
	require.NotEmpty(t, problems, "the 345 MHz current clock must not satisfy the OEM ceiling")
	assert.Contains(t, strings.Join(problems, "\n"), "N/A")
}

// A getter answering for only the first device must fail, naming the GPU.
func TestMaxCustomerBoostClockProblems_ChecksEveryGPU(t *testing.T) {
	out := xmlDocument(
		boostGPU("0000:07:00.0", "2070 MHz", "2070 MHz"),
		boostGPU("0000:0F:00.0", "2070 MHz", "N/A"),
	)

	problems := MaxCustomerBoostClockProblems(out, 2070)
	require.Len(t, problems, 1, "only the second GPU is unanswered")
	assert.Contains(t, problems[0], "0000:0F:00.0")
}

func TestMaxCustomerBoostClockProblems_RejectsUnparseableOutput(t *testing.T) {
	problems := MaxCustomerBoostClockProblems("not xml", 2070)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// The evidence the derive-from-graphics_max design rests on. Every real board
// reports max_customer_boost_clocks equal to max_clocks, which is why the mock
// resolves the OEM ceiling from clocks.graphics_max instead of carrying a key of
// its own. A capture that broke the equality would invalidate that, and this is
// where it would surface.
func TestHardwareCaptures_ReportCustomerBoostEqualToBoostMax(t *testing.T) {
	for _, file := range hardwareCaptures(t) {
		t.Run(filepath.Base(file), func(t *testing.T) {
			snap, err := ParseSnapshot(loadHardwareCapture(t, filepath.Base(file)))
			require.NoError(t, err)

			for i := range snap.Count() {
				gpu, err := snap.GPU(i)
				require.NoError(t, err)

				boost, ok := gpu.MaxCustomerBoostGraphicsClockMHz()
				require.True(t, ok, "%s: max_customer_boost_clocks/graphics_clock", gpu.Label())
				boostMax, ok := gpu.MaxGraphicsClockMHz()
				require.True(t, ok, "%s: max_clocks/graphics_clock", gpu.Label())

				require.Equal(t, boostMax, boost,
					"%s: the OEM ceiling differs from the boost maximum, so it cannot be derived from it",
					gpu.Label())
			}
		})
	}
}
