//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package e2e is the Go/Ginkgo end-to-end suite for nvml-mock. It is the Go
// port of docs/demo/standalone/demo.sh: ONE shared multi-node Kind cluster is
// created once, the nvml-mock chart is (re)installed per GPU profile via
// `helm upgrade --install` (a chart upgrade, NOT a cluster rebuild), and every
// profile's checks run against that same cluster. The suite owns the full
// lifecycle (cluster create/teardown, image build/load, helm, validation,
// diagnostics) behind one entrypoint that runs identically locally and in CI.
//
// It is gated by the `e2e` build tag so it never affects the fast
// `go test ./...` / `go build ./...` paths; run it with
// `ginkgo --tags e2e ./tests/e2e/go/...` (see the Makefile `e2e` target).
package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	// ClusterName is the single shared cluster the whole suite runs against.
	ClusterName = "nvml-mock-e2e"

	// nvmlMockNamespace isolates the chart under test from the default namespace.
	nvmlMockNamespace = "nvml-mock-system"
)

// builtImage is the nvml-mock image ref shared across parallel processes
// (built once on process #1 in SynchronizedBeforeSuite, then kind-loaded into
// the shared cluster).
var builtImage string

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "nvml-mock E2E Suite")
}

// SynchronizedBeforeSuite builds the nvml-mock image ONCE per runner (process
// #1) and shares the ref to every parallel process.
var _ = SynchronizedBeforeSuite(func() []byte {
	if config.SkipBuild() {
		return []byte(config.Image())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	buildImage(ctx)
	return []byte(config.Image())
}, func(data []byte) {
	builtImage = string(data)
})

func buildImage(ctx context.Context) {
	GinkgoHelper()
	args := []string{"buildx", "build", "-t", config.Image(), "-f", dockerfilePath(), "--load"}
	if gv := config.GolangVersion(); gv != "" {
		args = append(args, "--build-arg", "GOLANG_VERSION="+gv)
	}
	if config.BuildxGHACache() {
		args = append(args, "--cache-to", "type=gha,mode=max", "--cache-from", "type=gha")
	}
	args = append(args, repoRoot())
	By("building nvml-mock image " + config.Image())
	_, err := runner.Run(ctx, "docker", args...)
	Expect(err).NotTo(HaveOccurred(), "docker buildx build")
}

// ---------------------------------------------------------------------------
// Path resolution. `go test`/ginkgo run with the package dir as the working
// directory, so repo-relative paths (chart, Dockerfile, kind config, build
// context) are resolved against the module root discovered by walking up to
// go.mod.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Shared scenario helpers.
// ---------------------------------------------------------------------------

func loadProfile(name string) profile.Profile {
	GinkgoHelper()
	p, err := profile.Load(profilesDir(), name)
	Expect(err).NotTo(HaveOccurred(), "load profile %q", name)
	return p
}

// splitImage splits "repo:tag" into ("repo", "tag"), defaulting tag to latest.
func splitImage(ref string) (repo, tag string) {
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// installDemoChart (re)installs the nvml-mock release for a profile via
// `helm upgrade --install` onto the shared cluster, with the same integration
// flags the demo uses (fake GPU operator ConfigMaps + dynamic metrics).
func installDemoChart(ctx context.Context, h *harness.Harness, prof string, count int) {
	GinkgoHelper()
	rel := demoRelease(prof, count)
	By("helm upgrade --install nvml-mock (profile=" + prof + ")")
	err := h.Helm.UpgradeInstall(ctx, rel)
	Expect(err).NotTo(HaveOccurred(), "helm upgrade --install nvml-mock (profile=%s)", prof)
}

func demoRelease(prof string, count int) helm.Release {
	repo, tag := splitImage(config.Image())
	set := map[string]string{
		"image.repository":                     repo,
		"image.tag":                            tag,
		"integrations.fakeGpuOperator.enabled": "true",
		"gpu.profile":                          prof,
		"gpu.count":                            strconv.Itoa(count),
		"gpu.dynamicMetrics.enabled":           "true",
	}
	return helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		Set:             set,
		Wait:            true,
		Timeout:         config.HelmTimeout(),
	}
}

// firstNvmlPod returns the first nvml-mock pod in the dedicated e2e namespace.
func firstNvmlPod(ctx context.Context, h *harness.Harness) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		n, err := h.Kube.FirstPodName(ctx, nvmlMockNamespace, "app.kubernetes.io/name=nvml-mock")
		name = n
		return n, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no nvml-mock pod found")
	return kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
}

// podNode resolves the node (== Kind node container) a pod is scheduled on, for
// host-level docker-exec assertions (nvidia-smi, NVLink).
func podNode(ctx context.Context, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	n, err := h.Kube.PodNode(ctx, pod.Namespace, pod.Pod)
	Expect(err).NotTo(HaveOccurred())
	Expect(n).NotTo(BeEmpty(), "pod %s has no nodeName", pod.Pod)
	return n
}

// collectOnFailure writes diagnostics under artifacts/<sub...> when the current
// spec failed (mirrors the demo/bash "collect logs on failure" blocks).
func collectOnFailure(ctx context.Context, h *harness.Harness, sub ...string) {
	if !CurrentSpecReport().Failed() || h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, sub...)
	c.NvmlMockNamespace = nvmlMockNamespace
	c.Nodes = h.Nodes
	c.Common(ctx)
}

// setupCluster creates the shared cluster (delete-if-exists), wires adapters,
// kind-loads the image, and registers teardown + diagnostics cleanup.
func setupCluster(ctx context.Context, name string, kindConfig []byte, diagSub ...string) *harness.Harness {
	GinkgoHelper()
	h, err := harness.Setup(ctx, name, kindConfig, builtImage)
	DeferCleanup(func(ctx SpecContext) {
		collectOnFailure(ctx, h, diagSub...)
		_ = h.Teardown(ctx, config.KeepCluster())
	})
	Expect(err).NotTo(HaveOccurred(), "setup cluster %q", name)
	return h
}
