//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
)

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
		"gpu.count":                            strconv.Itoa(count),
		"gpu.dynamicMetrics.enabled":           "true",
		"gpu.profile":                          prof,
		"image.repository":                     repo,
		"image.tag":                            tag,
		"integrations.fakeGpuOperator.enabled": "true",
		// Steady, positive utilization so DCGM GPM PCIe throughput stays
		// nonzero on Hopper+; seed=1 for reproducibility.
		"gpu.dynamicMetrics.seed":                     "1",
		"gpu.dynamicMetrics.utilization.pattern":      "steady",
		"gpu.dynamicMetrics.utilization.gpu_min":      "50",
		"gpu.dynamicMetrics.utilization.gpu_max":      "50",
		"gpu.dynamicMetrics.utilization.memory_min":   "25",
		"gpu.dynamicMetrics.utilization.memory_max":   "25",
		"terminationGracePeriodSeconds":               "1",
		"updateStrategy.rollingUpdate.maxUnavailable": "100%",
	}
	return helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set:             set,
		Wait:            true,
		Timeout:         config.HelmTimeout(),
	}
}
