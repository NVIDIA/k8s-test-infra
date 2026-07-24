//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
)

func TestOSSuffixedRef(t *testing.T) {
	cases := []struct {
		ref   string
		osTag string
		want  string
	}{
		{"docker.io/library/mock-driver:e2e", "debian12", "docker.io/library/mock-driver:e2e-debian12"},
		{"mock-driver:e2e", "ubuntu2204", "mock-driver:e2e-ubuntu2204"},
		// registry host with a port keeps its colon in the repo portion.
		{"localhost:5000/mock-driver:e2e", "debian12", "localhost:5000/mock-driver:e2e-debian12"},
		// no explicit tag defaults to latest, then suffixed.
		{"docker.io/library/mock-driver", "debian12", "docker.io/library/mock-driver:latest-debian12"},
	}
	for _, c := range cases {
		t.Run(c.ref+"@"+c.osTag, func(t *testing.T) {
			if got := osSuffixedRef(c.ref, c.osTag); got != c.want {
				t.Fatalf("osSuffixedRef(%q, %q) = %q, want %q", c.ref, c.osTag, got, c.want)
			}
		})
	}
}

func TestGPUOperatorManagedDriverRelease(t *testing.T) {
	files := []string{"/tmp/baseline.yaml", "/tmp/driver.yaml"}
	rel := gpuOperatorManagedDriverRelease(files)

	if rel.Name != gpuOperatorRelease {
		t.Fatalf("expected release name %q, got %q", gpuOperatorRelease, rel.Name)
	}
	if rel.Chart != gpuOperatorChart {
		t.Fatalf("expected chart %q, got %q", gpuOperatorChart, rel.Chart)
	}
	if rel.Namespace != gpuOperatorNamespace || !rel.CreateNamespace {
		t.Fatalf("expected release to create namespace %q, got namespace=%q create=%v", gpuOperatorNamespace, rel.Namespace, rel.CreateNamespace)
	}
	// The version pin is load-bearing: the mock-driver only satisfies the
	// contract vendored for this operator version.
	if rel.Version != config.GPUOperatorVersion() {
		t.Fatalf("expected pinned version %q, got %q", config.GPUOperatorVersion(), rel.Version)
	}
	if !rel.Wait || rel.Timeout != 10*time.Minute {
		t.Fatalf("expected release to wait 10m, got wait=%v timeout=%s", rel.Wait, rel.Timeout)
	}
	if len(rel.ValuesFiles) != len(files) {
		t.Fatalf("expected %d values files, got %d", len(files), len(rel.ValuesFiles))
	}
	for i := range files {
		if rel.ValuesFiles[i] != files[i] {
			t.Fatalf("values file %d = %q, want %q (order is load-bearing: later files win)", i, rel.ValuesFiles[i], files[i])
		}
	}
}

func TestManagedDriverClusterNamesAreDedicated(t *testing.T) {
	names := []string{managedDriverClusterName, hostDriverClusterName}
	// Seed with every OTHER scenario's cluster name so a future collision with
	// any of them (not just the standalone/DRA/gpu-operator trio) is caught.
	seen := map[string]bool{
		ClusterName:            true,
		draClusterName:         true,
		gpuOperatorClusterName: true,
		nriClusterName:         true,
		multiNodeClusterName:   true,
	}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("cluster name %q collides with another scenario's cluster", n)
		}
		seen[n] = true
	}
}

func TestHostResidueCheckCoversInstalledPaths(t *testing.T) {
	cmd := hostResidueCheckCmd()
	for _, p := range hostResiduePaths {
		if !strings.Contains(cmd, p) {
			t.Fatalf("residue check missing path %q:\n%s", p, cmd)
		}
	}
	// stderr must be discarded so missing paths (the success case) don't print.
	if !strings.Contains(cmd, "2>/dev/null") {
		t.Fatalf("residue check must discard stderr:\n%s", cmd)
	}
	// The compound must end in a no-op `true` so an absent last path (the
	// clean-uninstall case) does not surface as a non-zero command exit.
	if !strings.HasSuffix(strings.TrimSpace(cmd), "; true") {
		t.Fatalf("residue check must pin exit status to 0 with a trailing `true`:\n%s", cmd)
	}
}
