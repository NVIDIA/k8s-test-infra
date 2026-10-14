// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// go-nvml and nvidia-smi size the array with a first call and fill it with a
// second; the count must come back in both.
func TestCopyIDs_TwoCallContract(t *testing.T) {
	t.Parallel()
	ids := []uint32{1001, 1002}

	var count uint32
	require.Equal(t, nvml.ERROR_INSUFFICIENT_SIZE, copyIDs[uint32](&count, nil, ids), "probe")
	require.Equal(t, uint32(2), count)

	short := uint32(1)
	buf := make([]uint32, 2)
	require.Equal(t, nvml.ERROR_INSUFFICIENT_SIZE, copyIDs(&short, &buf[0], ids))
	require.Equal(t, uint32(2), short)
	require.Equal(t, []uint32{0, 0}, buf, "nothing written into a short buffer")

	count = 2
	require.Equal(t, nvml.SUCCESS, copyIDs(&count, &buf[0], ids))
	require.Equal(t, ids, buf)

	empty := uint32(0)
	require.Equal(t, nvml.SUCCESS, copyIDs[uint32](&empty, nil, nil), "an empty list fits any buffer")
	require.Equal(t, uint32(0), empty)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, copyIDs[uint32](nil, nil, ids))
}
