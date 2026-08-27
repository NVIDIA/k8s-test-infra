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

func loadHardwareCapture(t *testing.T, name string) string {
	t.Helper()
	return loadFixture(t, filepath.Join("hardware", name))
}

// ccGPU renders one <gpu> element whose cc_protected_memory_usage block carries
// the given bodies verbatim, so a test can inject "0 MiB", "N/A" or nothing.
func ccGPU(id, total, used, free string) string {
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA GB300</product_name>
		<cc_protected_memory_usage>
			<total>` + total + `</total>
			<used>` + used + `</used>
			<free>` + free + `</free>
		</cc_protected_memory_usage>
	</gpu>`
}

// Every hardware capture must pass, which is what makes the expectation
// hardware-derived rather than chosen: the boards range from a Turing T4 that
// cannot do CC at all to a GB300 tray, and all of them report 0 MiB.
func TestConfComputeMemoryProblems_AcceptsEveryHardwareCapture(t *testing.T) {
	for _, file := range hardwareCaptures(t) {
		t.Run(filepath.Base(file), func(t *testing.T) {
			problems := ConfComputeMemoryProblems(loadHardwareCapture(t, filepath.Base(file)))
			assert.Empty(t, problems, strings.Join(problems, "; "))
		})
	}
}

// The defect this exists to catch: N/A says the driver answered nothing, where
// every real board reports 0 MiB. The mock captures were taken while both
// getters were generated stubs, so they read that way.
func TestConfComputeMemoryProblems_RejectsNotAvailableReadings(t *testing.T) {
	problems := ConfComputeMemoryProblems(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, "N/A")
	assert.Contains(t, joined, "getter is missing")
}

// CC mode is unmodelled, so a non-zero protected region cannot be reported —
// this keeps the check from degenerating into "any number will do".
func TestConfComputeMemoryProblems_RejectsNonZeroProtectedMemory(t *testing.T) {
	out := xmlDocument(ccGPU("0000:07:00.0", "1024 MiB", "0 MiB", "1024 MiB"))

	problems := ConfComputeMemoryProblems(out)
	require.Len(t, problems, 2, "total and free both claim a protected region")
	assert.Contains(t, strings.Join(problems, "\n"), "1024")
}

// Absent elements decode as empty bodies, which is a rename or a dropped block,
// not a zero.
func TestConfComputeMemoryProblems_ReportsMissingElements(t *testing.T) {
	out := xmlDocument(ccGPU("0000:07:00.0", "", "", ""))

	problems := ConfComputeMemoryProblems(out)
	require.Len(t, problems, 3)
	assert.Contains(t, strings.Join(problems, "\n"), `= ""`)
}

// A getter answering for only the first device must fail, naming the GPU.
func TestConfComputeMemoryProblems_ChecksEveryGPU(t *testing.T) {
	out := xmlDocument(
		ccGPU("0000:07:00.0", "0 MiB", "0 MiB", "0 MiB"),
		ccGPU("0000:0F:00.0", "N/A", "0 MiB", "0 MiB"),
	)

	problems := ConfComputeMemoryProblems(out)
	require.Len(t, problems, 1, "only the second GPU's total is N/A")
	assert.Contains(t, problems[0], "0000:0F:00.0")
}

// The block sits beside <fb_memory_usage>, which repeats every child name, so a
// check reading the wrong block would pass against the framebuffer's own zeros.
func TestConfComputeMemoryProblems_DoesNotReadTheFramebufferBlock(t *testing.T) {
	out := xmlDocument(`
	<gpu id="0000:07:00.0">
		<product_name>NVIDIA GB300</product_name>
		<fb_memory_usage>
			<total>0 MiB</total>
			<used>0 MiB</used>
			<free>0 MiB</free>
		</fb_memory_usage>
		<cc_protected_memory_usage>
			<total>N/A</total>
			<used>N/A</used>
			<free>N/A</free>
		</cc_protected_memory_usage>
	</gpu>`)

	problems := ConfComputeMemoryProblems(out)
	require.Len(t, problems, 3, "the framebuffer's zeros must not satisfy the CC block")
}

func TestConfComputeMemoryProblems_RejectsTruncatedOutput(t *testing.T) {
	full := xmlDocument(ccGPU("0000:07:00.0", "0 MiB", "0 MiB", "0 MiB"))
	truncated := full[:strings.Index(full, "<cc_protected_memory_usage>")]

	problems := ConfComputeMemoryProblems(truncated)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}
