//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateArgsUseStdinForKindConfig(t *testing.T) {
	args := createArgs("nvml-mock-e2e", true)

	idx := slices.Index(args, "--config")
	require.NotEqual(t, -1, idx, "expected --config when Kind config YAML is provided")
	require.Less(t, idx+1, len(args), "--config is missing its value")
	require.Equal(t, kindConfigStdinPath, args[idx+1], "expected Kind config to use stdin path")
}

func TestCreateArgsOmitConfigWithoutKindConfig(t *testing.T) {
	args := createArgs("nvml-mock-e2e", false)
	require.NotContains(t, args, "--config", "did not expect --config when Kind config YAML is empty")
}

func TestCreateArgsUseDefaultKubeconfig(t *testing.T) {
	args := createArgs("nvml-mock-e2e", true)
	require.NotContains(t, args, "--kubeconfig", "did not expect --kubeconfig; e2e should use the default kubeconfig")
}

func TestKindContext(t *testing.T) {
	require.Equal(t, "kind-nvml-mock-e2e", KindContext("nvml-mock-e2e"), "expected Kind context")
}
