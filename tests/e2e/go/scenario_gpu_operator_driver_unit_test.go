//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strings"
	"testing"
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

func TestManagedDriverClusterNamesAreDedicated(t *testing.T) {
	names := []string{managedDriverClusterName, managedDriverKmodClusterName, hostDriverClusterName}
	seen := map[string]bool{ClusterName: true, draClusterName: true, gpuOperatorClusterName: true}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("cluster name %q collides with another scenario's cluster", n)
		}
		seen[n] = true
	}
}

func TestStubKmodBuildScriptBakesVersionAndBuilds(t *testing.T) {
	script := stubKmodBuildScript("550.163.01")
	for _, want := range []string{
		`STUB_DRIVER_VERSION "550.163.01"`,
		"deployments/mock-driver/kmod/stub_version.h",
		`make -s -C "/lib/modules/$KVER/build"`,
		"test -f deployments/mock-driver/kmod/nvidia.ko",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("stub kmod build script missing %q:\n%s", want, script)
		}
	}
	// The entrypoint is prebuilt-only, so the build MUST NOT happen in the
	// container: the script runs host-side and never invokes the mock-driver
	// image's entrypoint.
	if strings.Contains(script, "nvidia-driver init") {
		t.Fatalf("stub kmod build script must not invoke the container entrypoint:\n%s", script)
	}
}

func TestKmodCheckPodRequestsNoGPU(t *testing.T) {
	manifest := string(kmodCheckPodManifest())
	if strings.Contains(manifest, "nvidia.com/gpu") {
		t.Fatalf("kmod-check pod must not request nvidia.com/gpu:\n%s", manifest)
	}
	for _, want := range []string{
		"name: kmod-check",
		"/proc/driver/nvidia/version",
		"/sys/module/nvidia/refcnt",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("kmod-check manifest missing %q:\n%s", want, manifest)
		}
	}
}

func TestHostResidueCheckCoversInstalledPaths(t *testing.T) {
	cmd := hostResidueCheckCmd()
	for _, p := range hostResiduePaths {
		if !strings.Contains(cmd, "ls -d "+p+" ") {
			t.Fatalf("residue check missing path %q:\n%s", p, cmd)
		}
	}
	// stderr must be discarded so missing paths (the success case) don't print.
	if !strings.Contains(cmd, "2>/dev/null") {
		t.Fatalf("residue check must discard stderr:\n%s", cmd)
	}
}
