//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strings"
	"testing"
)

func TestDRAResourceClaimManifest(t *testing.T) {
	manifest := string(draResourceClaimManifest())
	for _, want := range []string{
		"apiVersion: resource.k8s.io/v1beta1",
		"kind: ResourceClaimTemplate",
		"deviceClassName: gpu.nvidia.com",
		"name: gpu-test-pod",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("expected DRA ResourceClaim manifest to contain %q:\n%s", want, manifest)
		}
	}
}
