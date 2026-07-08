//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package helm

import "testing"

func TestBaseUsesDefaultKubeconfigWhenUnset(t *testing.T) {
	args := New("kind-nvml-mock-e2e").base()

	for _, arg := range args {
		if arg == "--kubeconfig" {
			t.Fatal("did not expect --kubeconfig when Helm should use the default kubeconfig")
		}
	}
}

func TestBaseTargetsKubeContext(t *testing.T) {
	args := New("kind-nvml-mock-e2e").base()

	if len(args) != 2 || args[0] != "--kube-context" || args[1] != "kind-nvml-mock-e2e" {
		t.Fatalf("expected Helm kube context args, got %#v", args)
	}
}
