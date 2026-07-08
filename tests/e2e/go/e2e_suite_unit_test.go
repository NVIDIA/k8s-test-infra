//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDemoReleaseTargetsDedicatedNamespace(t *testing.T) {
	oldRoot := cachedRoot
	cachedRoot = t.TempDir()
	t.Cleanup(func() {
		cachedRoot = oldRoot
	})

	rel := demoRelease("a100", 8)

	if rel.Namespace == "" {
		t.Fatal("expected nvml-mock release to target a dedicated namespace, got empty namespace")
	}
	if rel.Namespace == "default" {
		t.Fatal("expected nvml-mock release not to target default namespace")
	}
	if rel.Namespace != nvmlMockNamespace {
		t.Fatalf("expected nvml-mock release namespace %q, got %q", nvmlMockNamespace, rel.Namespace)
	}
	if !rel.CreateNamespace {
		t.Fatal("expected nvml-mock release to create its dedicated namespace")
	}
	if !rel.HideOutput {
		t.Fatal("expected nvml-mock release to hide Helm output")
	}
	if got := rel.Set["updateStrategy.rollingUpdate.maxUnavailable"]; got != "100%" {
		t.Fatalf("expected maxUnavailable 100%% for fast scenario rollouts, got %q", got)
	}
	if got := rel.Set["terminationGracePeriodSeconds"]; got != "1" {
		t.Fatalf("expected terminationGracePeriodSeconds=1 for fast scenario rollouts, got %q", got)
	}
}

func TestUseCaseLabels(t *testing.T) {
	want := []string{
		"labels",
		"fgo",
		"nvidia-smi",
		"nvlink",
		"ib",
		"pcisysfs",
		"ibping",
		"failure-injection",
	}
	if len(useCaseLabels) != len(want) {
		t.Fatalf("expected labels %#v, got %#v", want, useCaseLabels)
	}
	for i := range want {
		if useCaseLabels[i] != want[i] {
			t.Fatalf("expected labels %#v, got %#v", want, useCaseLabels)
		}
	}
}

func TestKindConfigPathForProfileUsesProfileOverride(t *testing.T) {
	root := t.TempDir()
	writeKindConfig(t, root, "kind.yaml")
	writeKindConfig(t, root, "kind-gb200.yaml")
	withRepoRoot(t, root)

	got, err := kindConfigPathForProfile("gb200")
	if err != nil {
		t.Fatalf("kind config path for profile: %v", err)
	}
	want := filepath.Join(root, "docs", "demo", "kind-gb200.yaml")
	if got != want {
		t.Fatalf("expected profile-specific kind config %q, got %q", want, got)
	}
}

func TestKindConfigPathForProfileFallsBackToDefault(t *testing.T) {
	root := t.TempDir()
	writeKindConfig(t, root, "kind.yaml")
	withRepoRoot(t, root)

	got, err := kindConfigPathForProfile("a100")
	if err != nil {
		t.Fatalf("kind config path for profile: %v", err)
	}
	want := filepath.Join(root, "docs", "demo", "kind.yaml")
	if got != want {
		t.Fatalf("expected default kind config %q, got %q", want, got)
	}
}

func TestSelectedKindConfigPathRejectsMixedConfigs(t *testing.T) {
	root := t.TempDir()
	writeKindConfig(t, root, "kind.yaml")
	writeKindConfig(t, root, "kind-gb200.yaml")
	withRepoRoot(t, root)

	if _, err := selectedKindConfigPath([]string{"a100", "gb200"}); err == nil {
		t.Fatal("expected mixed profile-specific Kind configs to fail")
	}
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir docs/demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("kind: Cluster\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
