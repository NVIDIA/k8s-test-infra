// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDriverMode(t *testing.T) {
	t.Parallel()

	tests := map[string]DriverMode{
		"":          DriverModeUserspace,
		"userspace": DriverModeUserspace,
		"kernel":    DriverModeKernel,
		" Kernel ":  DriverModeKernel,
	}
	for input, want := range tests {
		got, err := ParseDriverMode(input)
		require.NoError(t, err, "%q", input)
		require.Equal(t, want, got, "%q", input)
	}

	_, err := ParseDriverMode("kmod")
	require.ErrorContains(t, err, `invalid driver mode "kmod"`)
}
