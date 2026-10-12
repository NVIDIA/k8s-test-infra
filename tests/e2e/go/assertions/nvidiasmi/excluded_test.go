// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Real `nvidia-smi -B` output: nvidia-smi 580.65.06 against the mock with the
// gb300 profile, before and after `nvml-mock-ctl set --gpu 3 excluded=true`.
const (
	noExcludedGPUs  = "No excluded devices found.\n"
	oneExcludedGPU  = "Excluded GPU 0: 00000000:4B:00.0 (UUID: GPU-b300b300-0000-0000-0000-000000000003)\n"
	twoExcludedGPUs = oneExcludedGPU +
		"Excluded GPU 1: 00000000:4A:00.0 (UUID: GPU-b300b300-0000-0000-0000-000000000002)\n"
)

func TestParseExcludedGPUs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want []ExcludedGPU
	}{
		{name: "none", out: noExcludedGPUs, want: []ExcludedGPU{}},
		{
			name: "one",
			out:  oneExcludedGPU,
			want: []ExcludedGPU{{BusID: "00000000:4B:00.0", UUID: "GPU-b300b300-0000-0000-0000-000000000003"}},
		},
		{
			name: "in nvidia-smi order",
			out:  twoExcludedGPUs,
			want: []ExcludedGPU{
				{BusID: "00000000:4B:00.0", UUID: "GPU-b300b300-0000-0000-0000-000000000003"},
				{BusID: "00000000:4A:00.0", UUID: "GPU-b300b300-0000-0000-0000-000000000002"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseExcludedGPUs(tt.out)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// An answer that is neither the empty-list message nor an excluded-GPU line —
// an NVML error, say — must not read as "nothing excluded".
func TestParseExcludedGPUs_RejectsUnknownOutput(t *testing.T) {
	t.Parallel()

	_, err := ParseExcludedGPUs("Unable to determine the number of excluded GPUs: Not Supported\n")
	require.Error(t, err)
}
