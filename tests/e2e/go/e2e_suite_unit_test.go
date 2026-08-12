//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDemoReleaseTargetsDedicatedNamespace(t *testing.T) {
	oldRoot := cachedRoot
	cachedRoot = t.TempDir()
	t.Cleanup(func() {
		cachedRoot = oldRoot
	})

	rel := demoRelease("a100", 8)

	require.NotEmpty(t, rel.Namespace, "expected nvml-mock release to target a dedicated namespace, got empty namespace")
	require.NotEqual(t, "default", rel.Namespace, "expected nvml-mock release not to target default namespace")
	require.Equal(t, nvmlMockNamespace, rel.Namespace, "expected nvml-mock release namespace")
	require.True(t, rel.CreateNamespace, "expected nvml-mock release to create its dedicated namespace")
	require.True(t, rel.HideOutput, "expected nvml-mock release to hide Helm output")
	require.Equal(t, "100%", rel.Set["updateStrategy.rollingUpdate.maxUnavailable"],
		"expected maxUnavailable 100%% for fast scenario rollouts")
	require.Equal(t, "1", rel.Set["terminationGracePeriodSeconds"],
		"expected terminationGracePeriodSeconds=1 for fast scenario rollouts")
}

func TestKindConfigPathForProfileUsesProfileOverride(t *testing.T) {
	root := t.TempDir()
	writeKindConfig(t, root, "kind.yaml")
	writeKindConfig(t, root, "kind-gb200.yaml")
	withRepoRoot(t, root)

	got, err := kindConfigPathForProfile("gb200")
	require.NoError(t, err, "kind config path for profile")
	require.Equal(t, filepath.Join(root, "docs", "demo", "kind-gb200.yaml"), got,
		"expected profile-specific kind config")
}

func TestKindConfigPathForProfileFallsBackToDefault(t *testing.T) {
	root := t.TempDir()
	writeKindConfig(t, root, "kind.yaml")
	withRepoRoot(t, root)

	got, err := kindConfigPathForProfile("a100")
	require.NoError(t, err, "kind config path for profile")
	require.Equal(t, filepath.Join(root, "docs", "demo", "kind.yaml"), got,
		"expected default kind config")
}

func TestSelectedKindConfigPathRejectsMixedConfigs(t *testing.T) {
	root := t.TempDir()
	writeKindConfig(t, root, "kind.yaml")
	writeKindConfig(t, root, "kind-gb200.yaml")
	withRepoRoot(t, root)

	_, err := selectedKindConfigPath([]string{"a100", "gb200"})
	require.Error(t, err, "expected mixed profile-specific Kind configs to fail")
}

func withRepoRoot(t *testing.T, root string) {
	t.Helper()
	oldRoot := cachedRoot
	cachedRoot = root
	t.Cleanup(func() {
		cachedRoot = oldRoot
	})
}

func writeKindConfig(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "docs", "demo")
	require.NoError(t, os.MkdirAll(dir, 0o755), "mkdir docs/demo")
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("kind: Cluster\n"), 0o644),
		"write %s", name)
}
