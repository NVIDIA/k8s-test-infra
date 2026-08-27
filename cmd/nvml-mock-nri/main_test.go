// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitCSV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "empty string yields nothing", value: "", want: nil},
		{name: "single item", value: "kube-system", want: []string{"kube-system"}},
		{
			name:  "several items",
			value: "kube-system,nvidia-system",
			want:  []string{"kube-system", "nvidia-system"},
		},
		{
			// Catches dropping the TrimSpace: a namespace of " nvidia-system"
			// never matches, so the plugin injects into a namespace an operator
			// asked it to skip.
			name:  "trims surrounding whitespace",
			value: " kube-system , nvidia-system ",
			want:  []string{"kube-system", "nvidia-system"},
		},
		{
			// Catches dropping the empty check: an empty entry becomes an empty
			// namespace or an empty LD_PRELOAD path, which breaks the chain.
			name:  "skips empty fields",
			value: ",kube-system,,nvidia-system,",
			want:  []string{"kube-system", "nvidia-system"},
		},
		{name: "only separators yields nothing", value: ",,,", want: nil},
		{name: "only whitespace yields nothing", value: "  ,  ", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, splitCSV(tt.value))
		})
	}
}

// envOr uses os.Getenv, so these cannot run with t.Parallel alongside t.Setenv.
func TestEnvOr(t *testing.T) {
	t.Run("returns the environment value when set", func(t *testing.T) {
		t.Setenv("NVML_MOCK_TEST_KEY", "/from/env")
		require.Equal(t, "/from/env", envOr("NVML_MOCK_TEST_KEY", "/fallback"))
	})

	t.Run("returns the fallback when unset", func(t *testing.T) {
		require.Equal(t, "/fallback", envOr("NVML_MOCK_TEST_UNSET_KEY", "/fallback"))
	})

	t.Run("treats an empty value as unset", func(t *testing.T) {
		t.Setenv("NVML_MOCK_TEST_KEY", "")

		// Catches switching to os.LookupEnv. An empty value must fall back:
		// Kubernetes materialises an unset optional env var as "", and an empty
		// overlay path would make the plugin bind-mount the container root.
		require.Equal(t, "/fallback", envOr("NVML_MOCK_TEST_KEY", "/fallback"))
	})
}
