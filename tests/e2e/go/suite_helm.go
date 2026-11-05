//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// withSuiteFeatureGates adds the feature gates the job pins to the values of an
// nvml-mock install that does not reuse earlier values, so a reinstall cannot
// turn back on what the job turned off. Installs that reuse values keep the
// gates the job's own install set.
func withSuiteFeatureGates(set map[string]string) map[string]string {
	if !config.IMEXDownload() {
		set["featureGates.IMEXDownload"] = "false"
	}
	return set
}

// splitImage splits "repo:tag" into ("repo", "tag"), defaulting tag to latest.
func splitImage(ref string) (repo, tag string) {
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// installNvmlMock reinstalls the nvml-mock release on profile p and the image
// under test, with values layered on top, and waits for its DaemonSet. Empty
// values installs the profile alone. The install replaces the release's values
// rather than reusing them, so whatever a previous install layered on is gone.
func installNvmlMock(ctx context.Context, h *harness.Harness, p profile.Profile, values string) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	rel := helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: withSuiteFeatureGates(map[string]string{
			"gpu.count":        strconv.Itoa(p.ExpectedGPUs()),
			"gpu.profile":      p.Name,
			"image.repository": repo,
			"image.tag":        tag,
		}),
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
	if values != "" {
		path := filepath.Join(GinkgoT().TempDir(), "nvml-mock-values.yaml")
		Expect(os.WriteFile(path, []byte(values), 0o600)).To(Succeed())
		rel.ValuesFiles = []string{path}
	}
	By(fmt.Sprintf("helm upgrade --install nvml-mock (profile=%s, extra values=%t)", p.Name, values != ""))
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
}
