// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// The session getters write the caller's buffer element by element, so the C
// structs must be the size of the NVML header's, which go-nvml mirrors.
func TestSessionInfoLayouts_MatchGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, unsafe.Sizeof(nvml.EncoderSessionInfo{}), encoderSessionInfoSizeForTest(),
		"nvmlEncoderSessionInfo_t no longer matches go-nvml — ABI drift")
	require.Equal(t, unsafe.Sizeof(nvml.FBCSessionInfo{}), fbcSessionInfoSizeForTest(),
		"nvmlFBCSessionInfo_t no longer matches go-nvml — ABI drift")
}
