//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strings"
	"testing"
	"time"
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

func TestDRAClusterNameIsDedicated(t *testing.T) {
	if draClusterName == ClusterName {
		t.Fatalf("expected DRA cluster name to differ from standalone cluster name %q", ClusterName)
	}
	if draClusterName != "nvml-mock-dra" {
		t.Fatalf("expected DRA cluster name %q, got %q", "nvml-mock-dra", draClusterName)
	}
}

func TestDRADriverHelmRelease(t *testing.T) {
	rel := draDriverHelmRelease()

	if rel.Name != "nvidia-dra-driver" {
		t.Fatalf("expected DRA release name %q, got %q", "nvidia-dra-driver", rel.Name)
	}
	if rel.Chart != "nvidia/nvidia-dra-driver-gpu" {
		t.Fatalf("expected DRA chart %q, got %q", "nvidia/nvidia-dra-driver-gpu", rel.Chart)
	}
	if rel.Namespace != "nvidia" || !rel.CreateNamespace {
		t.Fatalf("expected DRA release to create namespace nvidia, got namespace=%q create=%v", rel.Namespace, rel.CreateNamespace)
	}
	if !rel.Wait || rel.Timeout != 3*time.Minute {
		t.Fatalf("expected DRA release to wait 3m, got wait=%v timeout=%s", rel.Wait, rel.Timeout)
	}

	wantSet := map[string]string{
		"nvidiaDriverRoot":                 "/var/lib/nvml-mock/driver",
		"gpuResourcesEnabledOverride":      "true",
		"resources.computeDomains.enabled": "false",
	}
	for key, want := range wantSet {
		if got := rel.Set[key]; got != want {
			t.Fatalf("expected DRA set %s=%q, got %q", key, want, got)
		}
	}
}
