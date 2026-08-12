// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Buggy a100 output from issue #635: T.Limit rows on Ampere, with signed
// margins rendered as absolute temperatures (negative / inverted shutdown).
const buggyPreAdaTemperatureQuery = `
    Temperature
        GPU Current Temp                  : 33 C
        GPU T.Limit Temp                  : 54 C
        GPU Shutdown T.Limit Temp         : -5 C
        GPU Slowdown T.Limit Temp         : 0 C
        GPU Max Operating T.Limit Temp    : 4 C
        GPU Target Temperature            : 83 C
        Memory Current Temp               : 31 C
`

// Real `nvidia-smi -q -d TEMPERATURE` output captured from the mock image with
// the architecture gate in place: a100 (ampere) and h100 (hopper). Pre-Ada
// keeps a "GPU T.Limit Temp : N/A" row, because the same nvidia-smi build
// prints that row whenever the margin API reports NOT_SUPPORTED — the defect is
// a T.Limit row carrying a NUMBER, not the label existing as N/A.
const fixedPreAdaTemperatureQuery = `
    Temperature
        GPU Current Temp                  : 33 C
        GPU T.Limit Temp                  : N/A
        GPU Shutdown Temp                 : 92 C
        GPU Slowdown Temp                 : 87 C
        GPU Max Operating Temp            : 83 C
        GPU Target Temperature            : 83 C
        Memory Current Temp               : 31 C
        Memory Max Operating Temp         : 83 C
`

const adaTemperatureQuery = `
    Temperature
        GPU Current Temp                  : 34 C
        GPU T.Limit Temp                  : 53 C
        GPU Shutdown T.Limit Temp         : -5 C
        GPU Slowdown T.Limit Temp         : 0 C
        GPU Max Operating T.Limit Temp    : 4 C
        GPU Target Temperature            : 83 C
        Memory Current Temp               : 32 C
        Memory Max Operating T.Limit Temp : N/A
`

func TestDiffTemperatureQuery_RejectsBuggyPreAdaOutput(t *testing.T) {
	problems := DiffTemperatureQuery(buggyPreAdaTemperatureQuery, false, 92, 87, 83)
	require.NotEmpty(t, problems, "buggy main output must fail the pre-Ada check")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "T.Limit")
	assert.Contains(t, joined, "missing absolute")
}

func TestDiffTemperatureQuery_AcceptsFixedPreAdaOutput(t *testing.T) {
	problems := DiffTemperatureQuery(fixedPreAdaTemperatureQuery, false, 92, 87, 83)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestDiffTemperatureQuery_AcceptsAdaTLimitOutput(t *testing.T) {
	problems := DiffTemperatureQuery(adaTemperatureQuery, true, 92, 87, 83)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestDiffTemperatureQuery_RejectsAbsoluteRowsOnAda(t *testing.T) {
	problems := DiffTemperatureQuery(fixedPreAdaTemperatureQuery, true, 92, 87, 83)
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "missing")
	assert.Contains(t, joined, "unexpected absolute")
}

func TestValidateNvidiaSMIEncoderFBCXML(t *testing.T) {
	const output = `
<nvidia_smi_log>
  <gpu id="00000000:01:00.0">
    <accounting_mode_buffer_size>4000</accounting_mode_buffer_size>
    <encoder_stats>
      <session_count>2</session_count>
      <average_fps>30</average_fps>
      <average_latency>1500 us</average_latency>
    </encoder_stats>
    <fbc_stats>
      <session_count>2</session_count>
      <average_fps>30</average_fps>
      <average_latency>1500 us</average_latency>
    </fbc_stats>
  </gpu>
</nvidia_smi_log>`
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	require.NoError(t, ValidateNvidiaSMIEncoderFBCXML(output, want, want, 4000))
}

func TestValidateNvidiaSMIEncoderFBCXMLRejectsStubValues(t *testing.T) {
	const output = `
<nvidia_smi_log>
  <gpu>
    <accounting_mode_buffer_size>N/A</accounting_mode_buffer_size>
    <encoder_stats>
      <session_count>N/A</session_count>
      <average_fps>N/A</average_fps>
      <average_latency>N/A</average_latency>
    </encoder_stats>
    <fbc_stats>
      <session_count>N/A</session_count>
      <average_fps>N/A</average_fps>
      <average_latency>N/A</average_latency>
    </fbc_stats>
  </gpu>
</nvidia_smi_log>`
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	err := ValidateNvidiaSMIEncoderFBCXML(output, want, want, 4000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected integer")
}
