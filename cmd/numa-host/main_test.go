// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRejectInvalidArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"apply", "true", "extra"}, {"apply", "invalid"}, {"unknown"}} {
		t.Run("args", func(t *testing.T) {
			t.Parallel()
			require.Error(t, run(t.Context(), args))
		})
	}
}
