//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strconv"
	"strings"

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
		Expect(nvidiasmi.ConfComputeQueryProblems(out, nvidiasmi.ConfComputeReport{
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
	It("moves a GPU's protected/unprotected split and keeps the others", func(ctx SpecContext) {
		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-sm", strconv.Itoa(ccUnprotectedKiB), "-i", "0")

		sizes, err := nvidiasmi.ParseConfComputeMemSizes(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gm"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sizes).To(HaveLen(p.ExpectedGPUs()))
		total := sizes[1].ProtectedKiB + sizes[1].UnprotectedKiB
		Expect(sizes[0]).To(Equal(nvidiasmi.ConfComputeMemSize{
			ProtectedKiB: total - ccUnprotectedKiB, UnprotectedKiB: ccUnprotectedKiB,
		}), "GPU 0 after -sm")
		Expect(sizes[1].UnprotectedKiB).To(BeZero(), "GPU 1 was not written")
		Expect(sizes[1].ProtectedKiB).To(BeNumerically(">", 0), "with CC on the whole framebuffer is protected")
	})

	It("reads back a key rotation threshold set by another process", func(ctx SpecContext) {
		adv, err := nvidiasmi.ParseConfComputeKeyRotation(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gkr"))
		Expect(err).NotTo(HaveOccurred())
		Expect(adv).To(BeEquivalentTo(60), "the driver default")

		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-skr", "55")
		adv, err = nvidiasmi.ParseConfComputeKeyRotation(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-gkr"))
		Expect(err).NotTo(HaveOccurred())
		Expect(adv).To(BeEquivalentTo(55))
	})

	It("accepts work once ready and then refuses to move the threshold or the split", func(ctx SpecContext) {
		// AfterAll's override reset returns the node to not ready.
		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-srs", "1")

		Expect(nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-grs")).To(ContainSubstring("Ready state: ready"))

		for _, args := range [][]string{
			{"conf-compute", "-skr", "50"},
			{"conf-compute", "-sm", "1024", "-i", "0"},
		} {
			res, err := h.Kube.ExecQuiet(ctx, nvmlPodOnNode(ctx, h, node), append([]string{"nvidia-smi"}, args...)...)
			Expect(err).To(HaveOccurred(), "nvidia-smi %s must fail while ready", strings.Join(args, " "))
			Expect(res.Combined()).To(ContainSubstring("Resource not in correct state"),
				"nvidia-smi %s", strings.Join(args, " "))
		}
	})
	It("reports Protected PCIe with CC off and persists the ready state", func(ctx SpecContext) {
		resetRuntimeOverridesOnNode(ctx, h, node)
		installNvmlMock(ctx, h, p, `gpu:
  confidentialCompute:
    mode: "off"
    multiGpuMode: protected_pcie
    cpuCaps: amd_sev_snp
`)
		want := nvidiasmi.ConfComputeReport{
			State: "OFF", MultiGPUMode: "Protected PCIe", CPUCaps: "AMD SEV-SNP",
			GPUCaps: "CC Capable", ReadyState: "Not Ready",
		}
		out := nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-q")
		Expect(nvidiasmi.ConfComputeQueryProblems(out, want)).To(BeEmpty(), "conf-compute -q:\n%s", out)

		nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-srs", "1")
		want.ReadyState = "Ready"
		out = nvidiaSMIOnNode(ctx, h, node, "conf-compute", "-q")
		Expect(nvidiasmi.ConfComputeQueryProblems(out, want)).To(BeEmpty(), "conf-compute -q:\n%s", out)
	})
})
