//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	devicePluginNamespace = "kube-system"
	devicePluginName      = "nvidia-device-plugin-mock"
	devicePluginSelector  = "name=nvidia-device-plugin-mock"

	validatorNamespace = "default"
	validatorJobName   = "gpu-validator-mock"
	validatorSelector  = "name=gpu-validator-mock"
)

// CUDA validator scenario. This is gated because the validator and device
// plugin images are pulled from nvcr.io.
var _ = Describe("nvml-mock CUDA validator", Label("validator"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		if !config.RunNGCSpecs() {
			Skip("set E2E_RUN_NGC=true to run nvcr.io-backed validator scenario")
		}
		h = setupCluster(ctx, ClusterName, demoKindConfig(selectedProfiles), "validator")
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p    profile.Profile
				node string
			)

			BeforeAll(func(ctx SpecContext) {
				p, _, node = setupStandaloneProfile(ctx, h, name)
				deployDevicePlugin(ctx, h, node, p.ExpectedGPUs())
			})

			It("runs CUDA vectorAdd against mock libcuda", func(ctx SpecContext) {
				runValidatorJob(ctx, h)
			})
		})
	}
})

func deployDevicePlugin(ctx SpecContext, h *harness.Harness, node string, expectedGPUs int) {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, assets.DevicePluginManifest)).To(Succeed(), "apply device plugin manifest")
	Expect(h.Kube.DeletePodsByLabel(ctx, devicePluginNamespace, devicePluginSelector)).To(Succeed(), "restart device plugin pods")
	assertions.WaitDaemonSetReady(ctx, h.Kube, devicePluginNamespace, devicePluginName, config.ReadyTimeout(), config.PollInterval())
	assertions.WaitAllocatableGPU(ctx, h.Kube, node, expectedGPUs, config.ReadyTimeout(), config.PollInterval())
}

func runValidatorJob(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Kube.Delete(ctx, assets.ValidatorManifest)).To(Succeed(), "delete previous validator job")
	Expect(h.Kube.Apply(ctx, assets.ValidatorManifest)).To(Succeed(), "apply validator job")
	assertions.WaitJobComplete(ctx, h.Kube, validatorNamespace, validatorJobName, config.ReadyTimeout(), config.PollInterval())
	if logs, err := h.Kube.Logs(ctx, validatorNamespace, validatorSelector, 100); err == nil {
		AddReportEntry("validator logs", logs)
	}
}
