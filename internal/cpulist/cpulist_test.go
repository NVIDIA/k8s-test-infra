// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cpulist

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	got, err := Parse("0-3,8,10-11\n")
	require.NoError(t, err)
	require.Equal(t, []int{0, 1, 2, 3, 8, 10, 11}, got)

	got, err = Parse("64-65, 96 - 97")
	require.NoError(t, err, "profiles may space their cpu_affinity, which the NVML engine accepts")
	require.Equal(t, []int{64, 65, 96, 97}, got)

	got, err = Parse("")
	require.NoError(t, err)
	require.Empty(t, got, "the kernel writes an empty list for a CPU-less node")

	for _, bad := range []string{"x", "3-1", "1-", "-1"} {
		_, err := Parse(bad)
		require.Error(t, err, "%q", bad)
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0-3,8,10-11", Format([]int{0, 1, 2, 3, 8, 10, 11}))
	require.Equal(t, "5", Format([]int{5}))
	require.Empty(t, Format(nil))
}
