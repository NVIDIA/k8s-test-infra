//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectedProfileNamesDefaultsToGB200(t *testing.T) {
	t.Setenv("E2E_PROFILES", "")
	require.Equal(t, []string{"gb200"}, SelectedProfileNames(), "expected default profile [gb200]")
}

func TestSelectedProfileNamesHonorsExplicitProfiles(t *testing.T) {
	t.Setenv("E2E_PROFILES", "a100, h100")
	require.Equal(t, []string{"a100", "h100"}, SelectedProfileNames(), "expected explicit profiles")
}

func TestArtifactsDirDefaultsToGoHarnessPath(t *testing.T) {
	t.Setenv("E2E_ARTIFACTS", "")
	require.Equal(t, "artifacts/e2e/go", ArtifactsDir(), "expected default artifacts dir")
}
