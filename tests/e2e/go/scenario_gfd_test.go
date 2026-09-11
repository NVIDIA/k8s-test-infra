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

	gfdNamespace = "kube-system"
	gfdName      = "nvidia-gfd-mock"
	gfdSelector  = "name=nvidia-gfd-mock"
)

// Standalone GPU Feature Discovery scenario. This is gated because the GFD
// image is pulled from nvcr.io; keep it disabled by default until #446
// resolves the CI image/auth path.
var _ = Describe("nvml-mock GPU feature discovery", Label("gfd"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		if !config.RunNGCSpecs() {
			Skip("set E2E_RUN_NGC=true to run the nvcr.io-backed GFD scenario; see #446")
		}
		h = setupCluster(ctx, "gfd")
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

			It("labels the node from the mock GPU inventory", func(ctx SpecContext) {
				deployGPUFeatureDiscovery(ctx, h, node)
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

func deployGPUFeatureDiscovery(ctx SpecContext, h *harness.Harness, node string) {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, assets.GFDManifest)).To(Succeed(), "apply GFD manifest")
	Expect(h.Kube.DeletePodsByLabel(ctx, gfdNamespace, gfdSelector)).To(Succeed(), "restart GFD pods")
	assertions.WaitDaemonSetReady(ctx, h.Kube, gfdNamespace, gfdName, config.ReadyTimeout(), config.PollInterval())
	assertions.WaitNodeLabelsPresent(ctx, h.Kube, node, gfdRequiredLabels(), config.ReadyTimeout(), config.PollInterval())
}

func gfdRequiredLabels() []string {
	return []string{
		"nvidia.com/gpu.product",
		"nvidia.com/gpu.memory",
		"nvidia.com/gpu.compute.major",
	}
}
