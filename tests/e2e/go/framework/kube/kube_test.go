//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kube

import "testing"

func TestBaseUsesDefaultKubeconfigWhenUnset(t *testing.T) {
	c, err := New("kind-nvml-mock-e2e")
	if err != nil {
		t.Fatalf("New default kubeconfig client: %v", err)
	}
	args := c.base()

	for _, arg := range args {
		if arg == "--kubeconfig" {
			t.Fatal("did not expect --kubeconfig when kubectl should use the default kubeconfig")
		}
	}
}

func TestBaseTargetsContext(t *testing.T) {
	c, err := New("kind-nvml-mock-e2e")
	if err != nil {
		t.Fatalf("New default kubeconfig client: %v", err)
	}
	args := c.base()

	if len(args) != 2 || args[0] != "--context" || args[1] != "kind-nvml-mock-e2e" {
		t.Fatalf("expected kubectl context args, got %#v", args)
	}
}
