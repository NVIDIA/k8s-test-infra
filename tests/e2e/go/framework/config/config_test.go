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

func TestArtifactsDirDefaultsToGoHarnessPath(t *testing.T) {
	t.Setenv("E2E_ARTIFACTS", "")

	if got := ArtifactsDir(); got != "artifacts/e2e/go" {
		t.Fatalf("expected default artifacts dir %q, got %q", "artifacts/e2e/go", got)
	}
}
