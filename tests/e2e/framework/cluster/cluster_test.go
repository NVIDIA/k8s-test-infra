//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cluster

import "testing"

func TestCreateArgsUseStdinForKindConfig(t *testing.T) {
	args := createArgs("nvml-mock-e2e", true)

	for i, arg := range args {
		if arg != "--config" {
			continue
		}
		if i+1 >= len(args) {
			t.Fatal("--config is missing its value")
		}
		if got := args[i+1]; got != kindConfigStdinPath {
			t.Fatalf("expected Kind config to use stdin path %q, got %q", kindConfigStdinPath, got)
		}
		return
	}
	t.Fatal("expected --config when Kind config YAML is provided")
}

func TestCreateArgsOmitConfigWithoutKindConfig(t *testing.T) {
	args := createArgs("nvml-mock-e2e", false)

	for _, arg := range args {
		if arg == "--config" {
			t.Fatal("did not expect --config when Kind config YAML is empty")
		}
	}
}

func TestCreateArgsUseDefaultKubeconfig(t *testing.T) {
	args := createArgs("nvml-mock-e2e", true)

	for _, arg := range args {
		if arg == "--kubeconfig" {
			t.Fatal("did not expect --kubeconfig; e2e should use the default kubeconfig")
		}
	}
}

func TestKindContext(t *testing.T) {
	if got := KindContext("nvml-mock-e2e"); got != "kind-nvml-mock-e2e" {
		t.Fatalf("expected Kind context %q, got %q", "kind-nvml-mock-e2e", got)
	}
}
