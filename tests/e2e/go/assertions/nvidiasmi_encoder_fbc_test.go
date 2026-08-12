// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiffEncoderFBCAccountingQuery_RejectsNA(t *testing.T) {
	out := `
    Encoder Stats
        Active Sessions                   : N/A
        Average FPS                       : N/A
        Average Latency                   : N/A
    FBC Stats
        Active Sessions                   : N/A
        Average FPS                       : N/A
        Average Latency                   : N/A
    Accounting Mode Buffer Size           : N/A
`
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	problems := DiffEncoderFBCAccountingQuery(out, want, want, 4000)
	require.NotEmpty(t, problems, "N/A stub output must fail against non-zero expected stats")
	require.Contains(t, problems[0], "N/A")
}

func TestDiffEncoderFBCAccountingQuery_AcceptsConfiguredValues(t *testing.T) {
	out := `
    Encoder Stats
        Active Sessions                   : 2
        Average FPS                       : 30
        Average Latency                   : 1500 us
    FBC Stats
        Active Sessions                   : 2
        Average FPS                       : 30
        Average Latency                   : 1500
    Accounting Mode Buffer Size           : 4000
`
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	problems := DiffEncoderFBCAccountingQuery(out, want, want, 4000)
	require.Empty(t, problems, "configured values should match: %v", problems)
}

func TestDiffEncoderFBCAccountingQuery_WrongNumberFails(t *testing.T) {
	out := `
    Encoder Stats
        Active Sessions                   : 0
        Average FPS                       : 0
        Average Latency                   : 0
    FBC Stats
        Active Sessions                   : 0
        Average FPS                       : 0
        Average Latency                   : 0
    Accounting Mode Buffer Size           : 4000
`
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	problems := DiffEncoderFBCAccountingQuery(out, want, want, 4000)
	require.NotEmpty(t, problems)
}
