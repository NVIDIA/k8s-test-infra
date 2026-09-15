// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gb200ProfileList is real `nvidia-smi power-profiles -l` output: nvidia-smi
// 580.65.06 against the mock configured with the gb200 profile. Two of the four
// GPU blocks are kept — enough to pin that the repetitions are split apart,
// since nvidia-smi prints them back to back with no header between.
const gb200ProfileList = `0. Max-P
1. Max-Q
2. Compute
3. Memory Bound
5. Balanced
6. LLM Inference
7. LLM Training
13. HPC
0. Max-P
1. Max-Q
2. Compute
3. Memory Bound
5. Balanced
6. LLM Inference
7. LLM Training
13. HPC
`

var gb200IDs = []int{0, 1, 2, 3, 5, 6, 7, 13}

func TestPowerProfileList_AcceptsRealOutput(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileListProblems(gb200ProfileList, 0, 2, gb200IDs))
}

// TestPowerProfileList_OrderIndependent guards against the check becoming a
// restatement of the config's ordering: nvidia-smi lists by ascending id
// regardless of the order the profile declares them in.
func TestPowerProfileList_OrderIndependent(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileListProblems(
		gb200ProfileList, 0, 2, []int{13, 0, 7, 1, 6, 2, 5, 3}))
}

// TestPowerProfileList_RejectsStubBehaviour is the regression this whole check
// exists for: both getters were generated stubs, so the subcommand refused.
func TestPowerProfileList_RejectsStubBehaviour(t *testing.T) {
	t.Parallel()
	problems := PowerProfileListProblems(
		"Workload Power Profiles feature is not supported on this device.\n", 3, 2, gb200IDs)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "exited 3, want 0")
}

func TestPowerProfileList_RejectsMissingProfile(t *testing.T) {
	t.Parallel()
	// The board dropped HPC on the second GPU.
	out := gb200ProfileList[:len(gb200ProfileList)-len("13. HPC\n")]
	problems := PowerProfileListProblems(out, 0, 2, gb200IDs)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "GPU 1 listed power profiles")
}

// TestPowerProfileList_RejectsInventedProfiles covers the uninitialised-tail
// bug. The bridge writes a 255-entry array into a buffer nvidia-smi never
// clears, so a tail left untouched renders profiles the board never advertised.
func TestPowerProfileList_RejectsInventedProfiles(t *testing.T) {
	t.Parallel()
	out := strings.Replace(gb200ProfileList,
		"13. HPC\n0. Max-P", "13. HPC\n14. MIG\n0. Max-P", 1)
	problems := PowerProfileListProblems(out, 0, 2, gb200IDs)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "GPU 0 listed power profiles")
	require.Contains(t, problems[0], "14")
}

func TestPowerProfileList_RejectsWrongGPUCount(t *testing.T) {
	t.Parallel()
	problems := PowerProfileListProblems(gb200ProfileList, 0, 4, gb200IDs)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "listed profiles for 2 GPU(s), want 4")
}

// TestPowerProfileList_SingleProfilePerGPU pins the block splitting in the case
// the "ids ascend within a block" heuristic is weakest: with one profile per
// GPU every line repeats the same id.
func TestPowerProfileList_SingleProfilePerGPU(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileListProblems(
		"0. Max-P\n0. Max-P\n0. Max-P\n", 0, 3, []int{0}))
}

// TestPowerProfileList_IgnoresDetailMetadata keeps the id regex from reading
// `-ld`'s indented metadata as profiles of its own.
func TestPowerProfileList_IgnoresDetailMetadata(t *testing.T) {
	t.Parallel()
	detailed := `0. Max-P
	Priority: 10
	Conflicts with:
		1. Max-Q
		5. Balanced
1. Max-Q
	Priority: 20
	Conflicts with:
		0. Max-P
`
	require.Empty(t, PowerProfileListProblems(detailed, 0, 1, []int{0, 1}))
}

func TestPowerProfileUnsupported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		out          string
		exit         int
		wantProblems bool
	}{
		{
			"device declines the feature",
			"Workload Power Profiles feature is not supported on this device.\n", 3, false,
		},
		{
			"pre-570 driver has no such symbol",
			"Unable to get Workload Power Profiles information: Function Not Found\n", 13, false,
		},
		{
			"listing profiles is a failure here",
			gb200ProfileList, 0, true,
		},
		{
			"failed for an unrelated reason",
			"Unable to determine the device handle for GPU0: Unknown Error\n", 255, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems := PowerProfileUnsupportedProblems(tc.out, tc.exit)
			if tc.wantProblems {
				require.NotEmpty(t, problems)
				return
			}
			require.Empty(t, problems)
		})
	}
}

// TestPowerProfileCurrent_AcceptsRealOutput uses the sentences nvidia-smi
// 580.65.06 printed against the mock. Every GB200, GB300 and B200 capture under
// testdata/hardware reports N/A for the requested and enforced profiles, which
// is the same state rendered through `-q -x`.
func TestPowerProfileCurrent_AcceptsRealOutput(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileCurrentProblems(
		"No profiles are currently requested.\n", 0,
		"No profiles are currently engaged.\n", 0))
}

func TestPowerProfileCurrent_RejectsFailures(t *testing.T) {
	t.Parallel()
	problems := PowerProfileCurrentProblems(
		"Workload Power Profiles feature is not supported on this device.\n", 3,
		"Workload Power Profiles feature is not supported on this device.\n", 3)
	require.Len(t, problems, 2)
	require.Contains(t, problems[0], "-gr exited 3")
	require.Contains(t, problems[1], "-ge exited 3")
}

// TestPowerProfileCurrent_RejectsUnexpectedlyEngaged catches a board reporting
// an enforced profile nobody asked for.
func TestPowerProfileCurrent_RejectsUnexpectedlyEngaged(t *testing.T) {
	t.Parallel()
	problems := PowerProfileCurrentProblems(
		"No profiles are currently requested.\n", 0,
		"0. Max-P\n", 0)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "-ge did not report")
}

// --- Set / clear round trip ---

// realRoundTripOutput is `nvidia-smi power-profiles -sr 6,13 -cr 6 -ge -i 0`
// against the gb200 profile. nvidia-smi orders the write before the enforced
// query, so the surviving profile is printed last.
const realRoundTripOutput = `Successfully set the requested profiles.
Successfully cleared the requested profiles.
13. HPC
`

func TestPowerProfileRoundTripAcceptsRealOutput(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileRoundTripProblems(realRoundTripOutput, 0, 1, []int{13}))
}

func TestPowerProfileRoundTripAcrossGPUs(t *testing.T) {
	t.Parallel()
	out := strings.Repeat(realRoundTripOutput, 4)
	require.Empty(t, PowerProfileRoundTripProblems(out, 0, 4, []int{13}))

	problems := PowerProfileRoundTripProblems(out, 0, 2, []int{13})
	require.NotEmpty(t, problems, "four GPUs' worth of output should not pass as two")
}

// TestPowerProfileRoundTripRejectsClearThatWipedEverything is the case the
// confirmation lines cannot catch: a clear that removed both profiles still
// reports success, and only the enforced read shows it.
func TestPowerProfileRoundTripRejectsClearThatWipedEverything(t *testing.T) {
	t.Parallel()
	out := `Successfully set the requested profiles.
Successfully cleared the requested profiles.
No profiles are currently engaged.
`
	problems := PowerProfileRoundTripProblems(out, 0, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "removed more than the profile it was given")
}

// TestPowerProfileRoundTripRejectsClearThatDidNothing is the opposite failure:
// the cleared profile is still enforced.
func TestPowerProfileRoundTripRejectsClearThatDidNothing(t *testing.T) {
	t.Parallel()
	out := `Successfully set the requested profiles.
Successfully cleared the requested profiles.
6. LLM Inference
13. HPC
`
	problems := PowerProfileRoundTripProblems(out, 0, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "enforced [6 13]")
}

func TestPowerProfileRoundTripRejectsMissingConfirmation(t *testing.T) {
	t.Parallel()
	// The set landed but the clear was never acknowledged.
	out := "Successfully set the requested profiles.\n13. HPC\n"
	problems := PowerProfileRoundTripProblems(out, 0, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "Successfully cleared the requested profiles.")
}

func TestPowerProfileRoundTripRejectsFailure(t *testing.T) {
	t.Parallel()
	problems := PowerProfileRoundTripProblems(
		"Workload Power Profiles feature is not supported on this device.", 2, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "exited 2")
}

// --- Conflict arbitration ---

// realArbitrationOutput is `nvidia-smi power-profiles -sr 0,5 -ge -i 0`, where
// Max-P and Balanced conflict and Max-P has the higher priority.
const realArbitrationOutput = `Successfully set the requested profiles.
0. Max-P
`

func TestPowerProfileArbitrationAcceptsRealOutput(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileArbitrationProblems(realArbitrationOutput, 0, 1, 0, 5))
}

// TestPowerProfileArbitrationRejectsEchoedRequest is what this assertion exists
// for: a mock that reported the request as enforced without arbitrating would
// list both profiles.
func TestPowerProfileArbitrationRejectsEchoedRequest(t *testing.T) {
	t.Parallel()
	out := "Successfully set the requested profiles.\n0. Max-P\n5. Balanced\n"
	problems := PowerProfileArbitrationProblems(out, 0, 1, 0, 5)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "want [0]")
}

func TestPowerProfileArbitrationRejectsWrongWinner(t *testing.T) {
	t.Parallel()
	out := "Successfully set the requested profiles.\n5. Balanced\n"
	problems := PowerProfileArbitrationProblems(out, 0, 1, 0, 5)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "enforced [5]")
}

// --- Rejected set ---

func TestPowerProfileSetRejectedAcceptsRealOutput(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileSetRejectedProblems(
		"Power Profile: 9 is not a supported profile number.", 2, 9))
}

func TestPowerProfileSetRejectedRejectsAcceptedWrite(t *testing.T) {
	t.Parallel()
	problems := PowerProfileSetRejectedProblems(
		"Successfully set the requested profiles.", 0, 9)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "want it refused")
}

// TestPowerProfileSetRejectedRejectsWrongReason keeps a refusal for some other
// reason, such as the whole feature being unsupported, from passing as a
// refusal of this one id.
func TestPowerProfileSetRejectedRejectsWrongReason(t *testing.T) {
	t.Parallel()
	problems := PowerProfileSetRejectedProblems(
		"Workload Power Profiles feature is not supported on this device.", 2, 9)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "did not report")
}

// --- Requested read in a later invocation ---

// realRequestedOutput is `nvidia-smi power-profiles -gr -i 0` against the gb200
// profile, run after a separate invocation asked for profile 13.
const realRequestedOutput = `13. HPC
`

func TestPowerProfileRequestedAcceptsRealOutput(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileRequestedProblems(realRequestedOutput, 0, 1, []int{13}))
}

// TestPowerProfileRequestedRejectsAWriteThatDidNotSurvive is the failure the
// assertion exists for: the request was set by another process, so a mock
// holding it in memory answers this read with the empty sentence.
func TestPowerProfileRequestedRejectsAWriteThatDidNotSurvive(t *testing.T) {
	t.Parallel()
	problems := PowerProfileRequestedProblems("No profiles are currently requested.\n", 0, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "did not outlive the invocation that set it")
}

func TestPowerProfileRequestedRejectsTheWrongProfile(t *testing.T) {
	t.Parallel()
	problems := PowerProfileRequestedProblems("6. LLM Inference\n", 0, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "requested [6]")
}

func TestPowerProfileRequestedRejectsNonZeroExit(t *testing.T) {
	t.Parallel()
	problems := PowerProfileRequestedProblems("boom", 1, 1, []int{13})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "-gr exited 1")
}

// TestPowerProfileRequestedAcceptsAnEmptyReadWhenNoneExpected keeps the
// cleanup path assertable: after clearing, nothing requested is the answer.
func TestPowerProfileRequestedAcceptsAnEmptyReadWhenNoneExpected(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerProfileRequestedProblems("No profiles are currently requested.\n", 0, 0, nil))
}
