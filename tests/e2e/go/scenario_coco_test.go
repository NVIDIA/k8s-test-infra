//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// ccProfile is fixed rather than iterated: CC needs Hopper or newer, and the
// scenario asserts the node-wide state, which no board changes.
const ccProfile = "h100"

// The CPU capability is non-default so a value dropped between Helm and NVML
// reads back as the default and fails. Protected PCIe is exercised separately
// because it cannot run alongside the CC feature.
const ccValues = `gpu:
  confidentialCompute:
    mode: "on"
    multiGpuMode: none
    cpuCaps: amd_sev_snp
`

// ccUnprotectedKiB is what the scenario moves GPU 0's unprotected memory to.
const ccUnprotectedKiB = 4194304

var _ = Describe("nvml-mock Confidential Compute", Label("cc"), Ordered, func() {
	var (
		h    *harness.Harness
		p    profile.Profile
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "cc")
		node = migTargetNode(ctx, h)
		p = loadProfile(ccProfile)
		installNvmlMock(ctx, h, p, ccValues)
		resetRuntimeOverridesOnNode(ctx, h, node)
	})

	AfterAll(func(ctx SpecContext) {
		resetRuntimeOverridesOnNode(ctx, h, node)
		installNvmlMock(ctx, h, p, "")
	})

	It("reports CC on with the configured mode and capabilities", func(ctx SpecContext) {
		out := nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-q")
		Expect(nvidiasmi.CoCoQueryProblems(out, nvidiasmi.CoCoReport{
			State:        "ON",
			MultiGPUMode: "None",
			CPUCaps:      "AMD SEV-SNP",
			GPUCaps:      "CC Capable",
			ReadyState:   "Not Ready",
		})).To(BeEmpty(), "conf-compute -q:\n%s", out)
		Expect(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-e")).To(ContainSubstring("CC Environment: PRODUCTION"))
	})

	// Every nvidia-smi exec is a fresh process, so each read-back below is a
	// write from one process observed by another.
	It("accepts a new protected/unprotected split without moving it", func(ctx SpecContext) {
		before, err := nvidiasmi.ParseCoCoMemSizes(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gm"))
		Expect(err).NotTo(HaveOccurred())
		Expect(before).To(HaveLen(p.ExpectedGPUs()))
		for i, size := range before {
			Expect(size.UnprotectedKiB).To(BeZero(), "GPU %d", i)
			Expect(size.ProtectedKiB).To(BeNumerically(">", 0), "with CC on the whole framebuffer of GPU %d is protected", i)
		}

		// The driver's handler for -sm only checks its locks: the command
		// succeeds and the allocator's split stands.
		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-sm", strconv.Itoa(ccUnprotectedKiB), "-i", "0")
		after, err := nvidiasmi.ParseCoCoMemSizes(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gm"))
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
	})

	It("reads back a key rotation threshold set by another process", func(ctx SpecContext) {
		adv, err := nvidiasmi.ParseCoCoKeyRotation(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gkr"))
		Expect(err).NotTo(HaveOccurred())
		Expect(adv).To(BeEquivalentTo(60), "the driver default")

		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-skr", "55")
		adv, err = nvidiasmi.ParseCoCoKeyRotation(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gkr"))
		Expect(err).NotTo(HaveOccurred())
		Expect(adv).To(BeEquivalentTo(55))
	})

	It("accepts work once ready, then refuses the threshold and revokes on leaving", func(ctx SpecContext) {
		// Leaving the ready state revokes it; the next spec's override reset
		// stands in for the driver reload that clears that.
		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-srs", "1")

		Expect(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-grs")).To(ContainSubstring("Ready state: ready"))

		res, err := h.Kube.ExecQuiet(ctx, nvmlPodOnNode(ctx, h, node), "nvidia-smi", "conf-compute", "-skr", "50")
		Expect(err).To(HaveOccurred(), "the threshold must not move while ready")
		Expect(res.Combined()).To(ContainSubstring("Resource not in correct state"))

		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-srs", "0")
		Expect(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-grs")).To(ContainSubstring("Ready state: not-ready"))
		res, err = h.Kube.ExecQuiet(ctx, nvmlPodOnNode(ctx, h, node), "nvidia-smi", "conf-compute", "-srs", "1")
		Expect(err).To(HaveOccurred(), "a revoked node must refuse to become ready again")
		Expect(res.Combined()).To(ContainSubstring("Invalid Argument"))
	})
	It("reports Protected PCIe with CC off and persists the ready state", func(ctx SpecContext) {
		resetRuntimeOverridesOnNode(ctx, h, node)
		installNvmlMock(ctx, h, p, `gpu:
  confidentialCompute:
    mode: "off"
    multiGpuMode: protected_pcie
    cpuCaps: amd_sev_snp
`)
		want := nvidiasmi.CoCoReport{
			State: "OFF", MultiGPUMode: "Protected PCIe", CPUCaps: "AMD SEV-SNP",
			GPUCaps: "CC Capable", ReadyState: "Not Ready",
		}
		out := nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-q")
		Expect(nvidiasmi.CoCoQueryProblems(out, want)).To(BeEmpty(), "conf-compute -q:\n%s", out)

		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-srs", "1")
		want.ReadyState = "Ready"
		out = nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-q")
		Expect(nvidiasmi.CoCoQueryProblems(out, want)).To(BeEmpty(), "conf-compute -q:\n%s", out)
	})
})
