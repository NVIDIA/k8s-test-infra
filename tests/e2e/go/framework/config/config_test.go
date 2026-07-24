//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestSelectedProfileNamesDefaultsToGB200(t *testing.T) {
	t.Setenv("E2E_PROFILES", "")

	got := SelectedProfileNames()
	if len(got) != 1 || got[0] != "gb200" {
		t.Fatalf("expected default profile [gb200], got %#v", got)
	}
}

func TestSelectedProfileNamesHonorsExplicitProfiles(t *testing.T) {
	t.Setenv("E2E_PROFILES", "a100, h100")

	got := SelectedProfileNames()
	want := []string{"a100", "h100"}
	if len(got) != len(want) {
		t.Fatalf("expected profiles %#v, got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected profiles %#v, got %#v", want, got)
		}
	}
}

func TestKeepClusterDefaultsToTrue(t *testing.T) {
	t.Setenv("E2E_KEEP_CLUSTER", "")

	if !KeepCluster() {
		t.Fatal("expected Kind cluster preservation to be enabled by default")
	}
}

func TestKeepClusterCanBeDisabled(t *testing.T) {
	for _, value := range []string{"0", "false", "no"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("E2E_KEEP_CLUSTER", value)

			if KeepCluster() {
				t.Fatalf("expected E2E_KEEP_CLUSTER=%q to disable Kind cluster preservation", value)
			}
		})
	}
}

func TestArtifactsDirDefaultsToGoHarnessPath(t *testing.T) {
	t.Setenv("E2E_ARTIFACTS", "")

	if got := ArtifactsDir(); got != "artifacts/e2e/go" {
		t.Fatalf("expected default artifacts dir %q, got %q", "artifacts/e2e/go", got)
	}
}

func TestMockDriverImageDefault(t *testing.T) {
	t.Setenv("E2E_MOCK_DRIVER_IMAGE", "")

	if got := MockDriverImage(); got != "docker.io/library/mock-driver:e2e" {
		t.Fatalf("expected default mock-driver image, got %q", got)
	}
}

func TestMockDriverImageOverride(t *testing.T) {
	t.Setenv("E2E_MOCK_DRIVER_IMAGE", "ttl.sh/mock-driver-abc:24h")

	if got := MockDriverImage(); got != "ttl.sh/mock-driver-abc:24h" {
		t.Fatalf("expected overridden mock-driver image, got %q", got)
	}
}

func TestGPUOperatorVersionDefaultsToContractPin(t *testing.T) {
	t.Setenv("E2E_GPU_OPERATOR_VERSION", "")

	if got := GPUOperatorVersion(); got != "v26.3.3" {
		t.Fatalf("expected default GPU Operator pin v26.3.3, got %q", got)
	}
}

func TestMockDriverSkipBuildDefaultsFalse(t *testing.T) {
	t.Setenv("E2E_MOCK_DRIVER_SKIP_BUILD", "")

	if MockDriverSkipBuild() {
		t.Fatal("expected mock-driver build not to be skipped by default")
	}
}
