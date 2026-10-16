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

// Standalone GPU Feature Discovery scenario. Opt-in through E2E_RUN_NGC and
// excluded from the default label filter, so no workflow runs it yet (#446).
// GFD now runs from the same k8s-device-plugin image the device plugin scenarios
// already pull in CI, so the nvcr.io access the gate was added for is no longer
// a blocker.
//
// GFD only writes a feature file; NFD turns it into node labels, so NFD is
// installed first and uninstalled afterwards. The NFD provenance scenario's first
// spec asserts its label is absent before NFD exists, so the uninstall keeps the
// two scenarios runnable on one cluster in either order.
var _ = Describe("nvml-mock GPU feature discovery", Label("gfd"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		if !config.RunNGCSpecs() {
			Skip("set E2E_RUN_NGC=true to run the nvcr.io-backed GFD scenario; see #446")
		}
		h = setupCluster(ctx, "gfd")
		installNFD(ctx, h)
		DeferCleanup(func(ctx SpecContext) {
			Expect(h.Helm.Uninstall(ctx, nfdRelease, nfdNamespace)).To(Succeed(), "uninstall NFD")
		})
	})

	for _, name := range selectedProfiles {
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p    profile.Profile
				node string
			)

			BeforeAll(func(ctx SpecContext) {
				p, _, _ = setupStandaloneProfile(ctx, h, name)
				// The NFD chart schedules no worker on the control plane, so GFD's
				// labels only ever reach a worker. setupStandaloneProfile's pod is
				// the first by name and can be the control-plane one.
				_, node = nvmlMockPodOnWorker(ctx, h)
				deployDevicePlugin(ctx, h, node, p.ExpectedGPUs())
			})

			It("labels the node from the mock GPU inventory", func(ctx SpecContext) {
				deployGPUFeatureDiscovery(ctx, h, node, p)
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

func deployGPUFeatureDiscovery(ctx SpecContext, h *harness.Harness, node string, p profile.Profile) {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, assets.GFDManifest)).To(Succeed(), "apply GFD manifest")
	Expect(h.Kube.DeletePodsByLabel(ctx, gfdNamespace, gfdSelector)).To(Succeed(), "restart GFD pods")
	assertions.WaitDaemonSetReady(ctx, h.Kube, gfdNamespace, gfdName, config.ReadyTimeout(), config.PollInterval())
	// Values from the profile, not merely the keys: every profile runs against
	// the same cluster, so a label the previous one left behind is present and
	// non-empty while still describing the wrong GPU.
	assertions.WaitGFDLabels(ctx, h.Kube, node,
		assertions.ExpectedGFDLabels(p.GFDProductName(), p.MemoryMiB(), p.ExpectedGPUs()),
		config.ReadyTimeout(), config.PollInterval())
}
