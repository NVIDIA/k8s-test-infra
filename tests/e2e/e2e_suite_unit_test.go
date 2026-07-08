//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import "testing"

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
}
