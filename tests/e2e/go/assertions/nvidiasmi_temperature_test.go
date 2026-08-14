// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// temperatureXML renders a one-GPU document whose <temperature> block carries
// the given element lines verbatim, so a test can inject a broken combination
// no healthy capture can show.
func temperatureXML(elements string) string {
	return `<?xml version="1.0" ?>
<nvidia_smi_log>
	<attached_gpus>1</attached_gpus>
	<gpu id="0000:07:00.0">
		<product_name>NVIDIA A100-SXM4-40GB</product_name>
		<temperature>` + elements + `
		</temperature>
	</gpu>
</nvidia_smi_log>`
}

// The issue #635 defect: T.Limit elements carrying numbers on Ampere, where the
// signed margins were rendered as absolute temperatures.
const buggyPreAdaTemperatureElements = `
			<gpu_temp>33 C</gpu_temp>
			<gpu_temp_tlimit>54 C</gpu_temp_tlimit>
			<gpu_temp_max_tlimit_threshold>-5 C</gpu_temp_max_tlimit_threshold>
			<gpu_temp_slow_tlimit_threshold>0 C</gpu_temp_slow_tlimit_threshold>
			<gpu_temp_max_gpu_tlimit_threshold>4 C</gpu_temp_max_gpu_tlimit_threshold>`

// An impossible absolute rendering: shutdown negative and below slowdown.
const invertedAbsoluteTemperatureElements = `
			<gpu_temp>33 C</gpu_temp>
			<gpu_temp_tlimit>N/A</gpu_temp_tlimit>
			<gpu_temp_max_threshold>-5 C</gpu_temp_max_threshold>
			<gpu_temp_slow_threshold>87 C</gpu_temp_slow_threshold>
			<gpu_temp_max_gpu_threshold>83 C</gpu_temp_max_gpu_threshold>`

func TestDiffTemperatureXML_AcceptsCapturedAmpereDocument(t *testing.T) {
	problems := DiffTemperatureXML(loadFixture(t, "qx-a100-healthy.xml"), false, 92, 87, 83)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestDiffTemperatureXML_AcceptsCapturedBlackwellDocument(t *testing.T) {
	problems := DiffTemperatureXML(loadFixture(t, "qx-gb200-healthy.xml"), true, 92, 87, 83)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestDiffTemperatureXML_RejectsTLimitElementsOnPreAda(t *testing.T) {
	problems := DiffTemperatureXML(temperatureXML(buggyPreAdaTemperatureElements), false, 92, 87, 83)
	require.NotEmpty(t, problems, "buggy pre-Ada output must fail the absolute check")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "tlimit")
	assert.Contains(t, joined, "missing absolute")
}

func TestDiffTemperatureXML_RejectsAbsoluteElementsOnAda(t *testing.T) {
	problems := DiffTemperatureXML(loadFixture(t, "qx-a100-healthy.xml"), true, 92, 87, 83)
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "missing")
	assert.Contains(t, joined, "unexpected absolute")
}

func TestDiffTemperatureXML_RejectsWrongThresholdValues(t *testing.T) {
	problems := DiffTemperatureXML(loadFixture(t, "qx-a100-healthy.xml"), false, 95, 87, 83)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "want 95 C")
}

func TestDiffTemperatureXML_RejectsImpossibleAbsoluteOrdering(t *testing.T) {
	problems := DiffTemperatureXML(temperatureXML(invertedAbsoluteTemperatureElements), false, -5, 87, 83)
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "negative")
	assert.Contains(t, joined, "below")
}

// A pre-Ada GPU legitimately reports gpu_temp_tlimit as N/A; only a T.Limit
// element carrying a NUMBER is the defect.
func TestDiffTemperatureXML_AcceptsNotAvailableTLimitOnPreAda(t *testing.T) {
	problems := DiffTemperatureXML(temperatureXML(`
			<gpu_temp>33 C</gpu_temp>
			<gpu_temp_tlimit>N/A</gpu_temp_tlimit>
			<gpu_temp_max_threshold>92 C</gpu_temp_max_threshold>
			<gpu_temp_slow_threshold>87 C</gpu_temp_slow_threshold>
			<gpu_temp_max_gpu_threshold>83 C</gpu_temp_max_gpu_threshold>`), false, 92, 87, 83)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestDiffTemperatureXML_ReportsUnparseableDocument(t *testing.T) {
	problems := DiffTemperatureXML("not xml", false, 92, 87, 83)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// A per-GPU getter that answers for only one device must be caught, so every
// GPU in the document is checked.
func TestDiffTemperatureXML_ChecksEveryGPU(t *testing.T) {
	problems := DiffTemperatureXML(loadFixture(t, "qx-gb200-healthy.xml"), false, 92, 87, 83)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "0000:0B:00.0", "the second GPU must be reported too")
}
