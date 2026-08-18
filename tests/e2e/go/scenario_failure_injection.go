//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
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

	snap := gpuSnapshot(ctx, h, pod)
	Expect(snap.Count()).To(Equal(expectedGPUs), "healthy baseline should describe all profile GPUs")
	Expect(snap.FailedGPUs()).To(BeEmpty(), "healthy baseline should report no failed devices")
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

	snap := gpuSnapshot(ctx, h, pod)
	Expect(snap.Count()).To(Equal(expectedGPUs), "ecc_uncorrectable keeps devices addressable")
	total, ok := snap.MaxUncorrectedECCAggregate()
	Expect(ok).To(BeTrue(), "ecc_uncorrectable should leave the counters readable, not fail the device")
	Expect(total).To(BeNumerically(">", 0), "ecc_uncorrectable should trip ECC counters")
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
	Expect(gpuSnapshot(ctx, h, pod).FailedGPUs()).
		NotTo(BeEmpty(), "lost mode should render an NVML error body in place of the readings")
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
	Expect(gpuSnapshot(ctx, h, pod).FailedGPUs()).
		NotTo(BeEmpty(), "fallen_off_bus should render an NVML error body in place of the readings")
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

// gpuSnapshot reads the whole `-q -x` document once. Every failure-injection
// check keys off it: the inventory, the ECC counters and the failed-device
// verdict all come from the same exec. An nvidia-smi that fails outright is
// reported as such, not read as a document with no errors in it.
func gpuSnapshot(ctx SpecContext, h *harness.Harness, pod kube.PodRef) nvidiasmi.Snapshot {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, pod)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x")
	return snap
}
