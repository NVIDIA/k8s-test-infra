//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

func assertFailureInjectionBaseline(ctx SpecContext, h *harness.Harness, pod kube.PodRef, expectedGPUs int) {
	GinkgoHelper()
	By("failure-injection healthy baseline")
	cfg := failureInjectionConfig(ctx, h)
	Expect(cfg).NotTo(ContainSubstring("failure:"), "healthy baseline should not render a failure block")
	Expect(nvidiaSMILCount(ctx, h, pod)).To(Equal(expectedGPUs), "healthy baseline should list all profile GPUs")
}

func assertECCUncorrectableFailure(ctx SpecContext, h *harness.Harness, expectedGPUs int) {
	GinkgoHelper()
	pod := upgradeFailureMode(ctx, h, "ecc_uncorrectable", map[string]string{
		"gpu.failureInjection.enabled":     "true",
		"gpu.failureInjection.mode":        "ecc_uncorrectable",
		"gpu.failureInjection.after_calls": "1",
		"gpu.failureInjection.xid.code":    "79",
	})
	assertConfigContains(ctx, h, "mode: ecc_uncorrectable")
	Expect(nvidiaSMILCount(ctx, h, pod)).To(Equal(expectedGPUs), "ecc_uncorrectable keeps devices addressable")
	Expect(maxIntegerLine(eccQuery(ctx, h, pod))).To(BeNumerically(">", 0), "ecc_uncorrectable should trip ECC counters")
}

func assertLostGPUFailure(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	pod := upgradeFailureMode(ctx, h, "lost", map[string]string{
		"gpu.failureInjection.enabled":     "true",
		"gpu.failureInjection.mode":        "lost",
		"gpu.failureInjection.after_calls": "1",
		"gpu.failureInjection.xid.code":    "0",
	})
	assertConfigContains(ctx, h, "mode: lost")
	Expect(hasFailureMarker(temperatureQuery(ctx, h, pod))).To(BeTrue(), "lost mode should surface nvidia-smi error markers")
}

func assertFallenOffBusFailure(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	pod := upgradeFailureMode(ctx, h, "fallen_off_bus", map[string]string{
		"gpu.failureInjection.enabled":     "true",
		"gpu.failureInjection.mode":        "fallen_off_bus",
		"gpu.failureInjection.after_calls": "1",
		"gpu.failureInjection.xid.code":    "79",
	})
	assertConfigContains(ctx, h, "mode: fallen_off_bus")
	assertConfigContains(ctx, h, "code: 79")
	Expect(hasFailureMarker(temperatureQuery(ctx, h, pod))).To(BeTrue(), "fallen_off_bus should surface nvidia-smi error markers")
}

func upgradeFailureMode(ctx SpecContext, h *harness.Harness, mode string, set map[string]string) kube.PodRef {
	GinkgoHelper()
	By("helm upgrade --reuse-values failure mode " + mode)
	err := h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:        "nvml-mock",
		Chart:       chartDir(),
		Namespace:   nvmlMockNamespace,
		HideOutput:  true,
		ReuseValues: true,
		Set:         set,
		Wait:        true,
		Timeout:     config.HelmTimeout(),
	})
	Expect(err).NotTo(HaveOccurred(), "helm upgrade failure mode %s", mode)
	Expect(h.Kube.DeletePodsByLabel(ctx, nvmlMockNamespace, nvmlMockSelector)).To(Succeed())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	return firstNvmlPod(ctx, h)
}

func failureInjectionConfig(ctx SpecContext, h *harness.Harness) string {
	GinkgoHelper()
	out, err := h.Kube.ConfigMapData(ctx, nvmlMockNamespace, "nvml-mock-config", "config.yaml")
	Expect(err).NotTo(HaveOccurred(), "read nvml-mock configmap")
	return out
}

func assertConfigContains(ctx SpecContext, h *harness.Harness, needle string) {
	GinkgoHelper()
	Expect(failureInjectionConfig(ctx, h)).To(ContainSubstring(needle), "ConfigMap should contain %q", needle)
}

func nvidiaSMILCount(ctx SpecContext, h *harness.Harness, pod kube.PodRef) int {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "-L")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L: %s", res.Combined())
	count := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "GPU ") {
			count++
		}
	}
	return count
}

func eccQuery(ctx SpecContext, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	res, _ := h.Kube.Exec(ctx, pod, "nvidia-smi", "--query-gpu=ecc.errors.uncorrected.aggregate.total", "--format=csv,noheader,nounits")
	return res.Combined()
}

func temperatureQuery(ctx SpecContext, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	res, _ := h.Kube.Exec(ctx, pod, "nvidia-smi", "--query-gpu=temperature.gpu", "--format=csv,noheader,nounits")
	return res.Combined()
}

func maxIntegerLine(out string) int {
	max := 0
	for _, line := range strings.Split(out, "\n") {
		v, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && v > max {
			max = v
		}
	}
	return max
}

func hasFailureMarker(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "[n/a]") ||
		strings.Contains(lower, "[unknown error]") ||
		strings.Contains(lower, "gpu is lost") ||
		strings.Contains(lower, "err")
}
