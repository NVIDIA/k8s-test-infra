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
