//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// Path helpers resolve repo-relative files from the package working directory
// used by `go test`/Ginkgo.
var cachedRoot string

func repoRoot() string {
	if cachedRoot != "" {
		return cachedRoot
	}
	if env := strings.TrimSpace(os.Getenv("E2E_REPO_ROOT")); env != "" {
		cachedRoot = env
		return cachedRoot
	}
	dir, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			cachedRoot = dir
			return cachedRoot
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	Fail("could not locate repo root (go.mod) from working directory")
	return ""
}

func chartDir() string {
	return filepath.Join(repoRoot(), "deployments", "nvml-mock", "helm", "nvml-mock")
}

func profilesDir() string {
	return filepath.Join(chartDir(), "profiles")
}

func loadProfile(name string) profile.Profile {
	GinkgoHelper()
	p, err := profile.Load(profilesDir(), name)
	Expect(err).NotTo(HaveOccurred(), "load profile %q", name)
	return p
}
