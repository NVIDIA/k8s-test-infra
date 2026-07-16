//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
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

func dockerfilePath() string {
	return filepath.Join(repoRoot(), "deployments", "nvml-mock", "Dockerfile")
}

// demoKindConfig is the shared multi-node cluster config. It defaults to
// docs/demo/kind.yaml and allows profile-specific overrides at
// docs/demo/kind-<profile>.yaml for profiles that need special cluster wiring.
func demoKindConfig(profiles []string) []byte {
	path, err := selectedKindConfigPath(profiles)
	Expect(err).NotTo(HaveOccurred())
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "read demo kind config %s", path)
	return data
}

func selectedKindConfigPath(profiles []string) (string, error) {
	var selected string
	for _, profileName := range profiles {
		path, err := kindConfigPathForProfile(profileName)
		if err != nil {
			return "", err
		}
		if selected == "" {
			selected = path
			continue
		}
		if path != selected {
			return "", fmt.Errorf("profiles %q and %q require different Kind configs (%s vs %s); run them in separate E2E_PROFILES invocations", profiles[0], profileName, selected, path)
		}
	}
	if selected == "" {
		return kindConfigPathForProfile("")
	}
	return selected, nil
}

func kindConfigPathForProfile(profileName string) (string, error) {
	defaultPath := filepath.Join(repoRoot(), "docs", "demo", "kind.yaml")
	if profileName == "" {
		return defaultPath, nil
	}
	profilePath := filepath.Join(repoRoot(), "docs", "demo", "kind-"+profileName+".yaml")
	if _, err := os.Stat(profilePath); err == nil {
		return profilePath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat profile Kind config %s: %w", profilePath, err)
	}
	return defaultPath, nil
}

func loadProfile(name string) profile.Profile {
	GinkgoHelper()
	p, err := profile.Load(profilesDir(), name)
	Expect(err).NotTo(HaveOccurred(), "load profile %q", name)
	return p
}
