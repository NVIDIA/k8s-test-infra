// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBranchProblems(t *testing.T) {
	t.Parallel()
	// The banner rows of the two bundled binaries, which label the driver
	// version differently.
	const (
		banner580 = "| NVIDIA-SMI 580.65.06              Driver Version: 580.65.06      CUDA Version: 13.0     |"
		banner615 = "| NVIDIA-SMI 615.71.09              KMD Version: 615.71.09     CUDA UMD Version: 13.4     |"
	)
	require.Empty(t, BranchProblems(banner580, 580))
	require.Empty(t, BranchProblems(banner615, 615))

	problems := BranchProblems(banner580, 615)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "branch 580")
	require.Contains(t, problems[0], "branch 615")

	require.Len(t, BranchProblems("command not found", 580), 1)
}
