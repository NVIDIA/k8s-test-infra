// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Buggy output from issue #637: the profile configures utilization.jpeg and
// utilization.ofa, both are parsed, and nvidia-smi still prints N/A because the
// NVML entry points were generated stubs.
const buggyJpgOfaUtilizationQuery = `
    Utilization
        GPU                               : 21 %
        Memory                            : 22 %
        Encoder                           : 0 %
        Decoder                           : 0 %
        JPEG                              : N/A
        OFA                               : N/A
`

// Real `nvidia-smi -q -d UTILIZATION` output captured from the mock image with
// the getters in place, against a config setting jpeg: 35 / ofa: 12. The
// trailing "JPG/OFA Utilization Samples" sections are kept because their
// labels also start with the row names being matched.
const fixedJpgOfaUtilizationQuery = `
GPU 0000:07:00.0
    Utilization
        GPU                               : 0 %
        Memory                            : 0 %
        Encoder                           : 0 %
        Decoder                           : 0 %
        JPEG                              : 35 %
        OFA                               : 12 %
    JPG Utilization Samples
        Duration                          : N/A
        Number of Samples                 : N/A
    OFA Utilization Samples
        Duration                          : N/A
        Number of Samples                 : N/A
`

func TestDiffJpgOfaUtilizationQuery_AcceptsConfiguredPercentages(t *testing.T) {
	require.Empty(t, DiffJpgOfaUtilizationQuery(fixedJpgOfaUtilizationQuery, 35, 12))
}

func TestDiffJpgOfaUtilizationQuery_RejectsNotAvailableRows(t *testing.T) {
	problems := DiffJpgOfaUtilizationQuery(buggyJpgOfaUtilizationQuery, 35, 12)
	require.Len(t, problems, 2, "both rows are N/A")
	require.Contains(t, strings.Join(problems, "\n"), `JPEG = "N/A"`)
	require.Contains(t, strings.Join(problems, "\n"), `OFA = "N/A"`)
}

// A zeroed default reading must not satisfy a non-zero expectation, and the two
// values must not be interchangeable — the reason the fixture uses 35 and 12.
func TestDiffJpgOfaUtilizationQuery_RejectsWrongPercentages(t *testing.T) {
	require.Len(t, DiffJpgOfaUtilizationQuery(fixedJpgOfaUtilizationQuery, 0, 0), 2,
		"35 %% / 12 %% must not satisfy a zeroed expectation")
	require.Len(t, DiffJpgOfaUtilizationQuery(fixedJpgOfaUtilizationQuery, 12, 35), 2,
		"transposed expectations must not pass")
}

func TestDiffJpgOfaUtilizationQuery_ReportsMissingRows(t *testing.T) {
	problems := DiffJpgOfaUtilizationQuery("    Utilization\n        GPU : 0 %\n", 35, 12)
	require.Len(t, problems, 2)
	require.Contains(t, strings.Join(problems, "\n"), `missing "JPEG" row`)
	require.Contains(t, strings.Join(problems, "\n"), `missing "OFA" row`)
}

// Multi-GPU output: a getter that answers for only the first device must fail.
func TestDiffJpgOfaUtilizationQuery_ChecksEveryGPU(t *testing.T) {
	twoGPUs := fixedJpgOfaUtilizationQuery + strings.Replace(
		fixedJpgOfaUtilizationQuery, "JPEG                              : 35 %",
		"JPEG                              : N/A", 1)
	problems := DiffJpgOfaUtilizationQuery(twoGPUs, 35, 12)
	require.Len(t, problems, 1, "the second GPU's JPEG row is N/A")
}
