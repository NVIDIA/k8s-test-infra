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

// The node's host interface cards: the one newly implemented system surface
// nvidia-smi renders, through `-q -u`.
//
// The cards are a chart value the library only reads from its config at load,
// so declaring them takes a Helm upgrade and a pod restart rather than an
// nvml-mock-ctl override.

// configuredHICs are the cards the upgraded release declares. Two, with
// distinct ids and firmware strings, so neither a dropped entry nor a swapped
// pair can pass.
func configuredHICs() []nvidiasmi.HIC {
	return []nvidiasmi.HIC{
		{ID: "0", Firmware: "1.2.3.4"},
		{ID: "1", Firmware: "5.6.7.8"},
	}
}

// upgradeWithHICs re-deploys the release with configuredHICs declared and
// restores the profile's own values afterwards, then returns a restarted pod.
func upgradeWithHICs(ctx SpecContext, h *harness.Harness) kube.PodRef {
	GinkgoHelper()

	set := map[string]string{
		"hic[0].id":              configuredHICs()[0].ID,
		"hic[0].firmwareVersion": configuredHICs()[0].Firmware,
		"hic[1].id":              configuredHICs()[1].ID,
		"hic[1].firmwareVersion": configuredHICs()[1].Firmware,
	}
	DeferCleanup(func(ctx SpecContext) {
		upgradeHICs(ctx, h, "restore the profile's own cards", map[string]string{"hic": "null"})
	})
	return upgradeHICs(ctx, h, "declare 2 host interface cards", set)
}

// upgradeHICs runs `helm upgrade --reuse-values` with set, restarts the
// DaemonSet pods and returns one of the new ones.
func upgradeHICs(ctx SpecContext, h *harness.Harness, reason string, set map[string]string) kube.PodRef {
	GinkgoHelper()
	By("helm upgrade --reuse-values to " + reason)

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
	Expect(err).NotTo(HaveOccurred(), "helm upgrade to %s", reason)
	Expect(h.Kube.DeletePodsByLabel(ctx, nvmlMockNamespace, nvmlMockSelector)).To(Succeed())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	return firstNvmlPod(ctx, h)
}
