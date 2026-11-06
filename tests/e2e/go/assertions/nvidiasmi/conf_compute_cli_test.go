// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These fixtures follow the nvidia-smi 615.71.10 report layout. CC-on uses
// H100 without multi-GPU protection; Protected PCIe requires the CC feature off.
const (
	ccOnQuery = `
==============NVSMI CONF-COMPUTE LOG==============

    CC State                   : ON
    Multi-GPU Mode             : None
    CPU CC Capabilities        : AMD SEV-SNP
    GPU CC Capabilities        : CC Capable
    CC GPUs Ready State        : Not Ready

`
	ccOnReadyQuery = `
==============NVSMI CONF-COMPUTE LOG==============

    CC State                   : ON
    Multi-GPU Mode             : None
    CPU CC Capabilities        : AMD SEV-SNP
    GPU CC Capabilities        : CC Capable
    CC GPUs Ready State        : Ready

`
	ccOffQuery = `
==============NVSMI CONF-COMPUTE LOG==============

    CC State                   : OFF
    Multi-GPU Mode             : None
    CPU CC Capabilities        : None
    GPU CC Capabilities        : CC Capable
    CC GPUs Ready State        : Not Ready

`
	ccT4Feature = `No CC capable devices found.
`
	ccOnMemSizes = `GPU 0: NVIDIA H100 80GB HBM3 (UUID: GPU-01000100-0000-0000-0000-000000000000)
Protected memory size:   79691776 KiB
Unprotected memory size: 4194304 KiB
`
	ccOffMemSizes = `GPU 0: NVIDIA H100 80GB HBM3 (UUID: GPU-01000100-0000-0000-0000-000000000000)
Protected memory size:   0 KiB
Unprotected memory size: 0 KiB
`
	ccOnKeyRotation = `
Confidential Compute Key Rotation Threshold Attacker Advantage: 55

`
	ccOnFeature = `CC status: ON
`
)

// The values the on- captures' profile configures, as nvidia-smi renders them.
var ccOnReport = ConfComputeReport{
	State:        "ON",
	MultiGPUMode: "None",
	CPUCaps:      "AMD SEV-SNP",
	GPUCaps:      "CC Capable",
	ReadyState:   "Not Ready",
}

func TestConfComputeQueryProblems_AcceptsCaptures(t *testing.T) {
	t.Parallel()

	assert.Empty(t, ConfComputeQueryProblems(ccOnQuery, ccOnReport))

	ready := ccOnReport
	ready.ReadyState = "Ready"
	assert.Empty(t, ConfComputeQueryProblems(ccOnReadyQuery, ready))

	assert.Empty(t, ConfComputeQueryProblems(ccOffQuery, ConfComputeReport{
		State: "OFF", MultiGPUMode: "None", CPUCaps: "None", GPUCaps: "CC Capable", ReadyState: "Not Ready",
	}))
}

// Each field is compared on its own, so a report that is right except for the
// ready state names that row rather than failing as a whole.
func TestConfComputeQueryProblems_NamesTheWrongRow(t *testing.T) {
	t.Parallel()

	want := ccOnReport
	want.ReadyState = "Ready"
	problems := ConfComputeQueryProblems(ccOnQuery, want)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "CC GPUs Ready State")
	assert.Contains(t, problems[0], `"Not Ready"`)
}

// A report cut short — nvidia-smi aborts -q on the first getter that
// declines — must fail on the rows it never printed, not pass on the ones it did.
func TestConfComputeQueryProblems_ReportsMissingRows(t *testing.T) {
	t.Parallel()

	out := strings.Replace(ccOnQuery, "    CC GPUs Ready State        : Not Ready\n", "", 1)
	problems := ConfComputeQueryProblems(out, ccOnReport)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "missing")
}

func TestConfComputeQueryProblems_RejectsAbsentLog(t *testing.T) {
	t.Parallel()

	problems := ConfComputeQueryProblems(ccT4Feature, ccOnReport)
	require.NotEmpty(t, problems)
	assert.Contains(t, problems[0], "No CC capable devices found")
}

func TestParseConfComputeMemSizes(t *testing.T) {
	t.Parallel()

	sizes, err := ParseConfComputeMemSizes(ccOnMemSizes)
	require.NoError(t, err)
	require.Equal(t, map[int]ConfComputeMemSize{0: {ProtectedKiB: 79691776, UnprotectedKiB: 4194304}}, sizes)

	sizes, err = ParseConfComputeMemSizes(ccOffMemSizes)
	require.NoError(t, err)
	require.Equal(t, map[int]ConfComputeMemSize{0: {}}, sizes)

	_, err = ParseConfComputeMemSizes(ccT4Feature)
	require.Error(t, err, "no GPU rows")
}

func TestParseConfComputeKeyRotation(t *testing.T) {
	t.Parallel()

	adv, err := ParseConfComputeKeyRotation(ccOnKeyRotation)
	require.NoError(t, err)
	require.EqualValues(t, 55, adv)

	_, err = ParseConfComputeKeyRotation(ccOnFeature)
	require.Error(t, err)
}
