//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	// vgpuProfile is fixed rather than iterated: the type list is the
	// install's, so the board under it changes nothing the scenario asserts.
	vgpuProfile         = "a100"
	vgpuTypeName        = "NVIDIA A100-4C"
	vgpuInstancesPerGPU = 2
	vgpuHostDriver      = "550.163.01"
	vgpuHostBranch      = "r550_00"
	vgpuLicensedProduct = "NVIDIA Virtual Compute Server"
)

const vgpuHostValues = `gpu:
  vgpu:
    mode: host
    hostVgpuMode: sriov
    types:
      - id: 474
        name: "NVIDIA A100-4C"
        framebuffer_mib: 4096
        max_instances: 10
        license: "NVIDIA-Virtual-Compute-Server,9.0"
    instances:
      - type: "NVIDIA A100-4C"
        count: 2
`

const vgpuGuestValues = `gpu:
  vgpu:
    mode: guest
    guest:
      host_driver_version: "550.163.01"
      host_driver_branch: "r550_00"
      license:
        product: "NVIDIA Virtual Compute Server"
        state: licensed
`

// vGPU scenario (#376). Both sides of the hypervisor are asserted through the
// consumers that read them on real hardware: nvidia-smi on the host, and
// nvidia-smi plus the GPU Operator's GPU Feature Discovery in the guest, whose
// vgpu.* labels come from the PCI capability rather than from NVML.
var _ = Describe("nvml-mock vGPU", Label("vgpu"), Ordered, func() {
	var (
		h    *harness.Harness
		p    profile.Profile
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "vgpu")
		node = migTargetNode(ctx, h)
		p = loadProfile(vgpuProfile)
	})

	AfterAll(func(ctx SpecContext) {
		installNvmlMock(ctx, h, p, "")
	})

	Context("host", Label("vgpu-host"), Ordered, func() {
		BeforeAll(func(ctx SpecContext) {
			installNvmlMock(ctx, h, p, vgpuHostValues)
		})

		It("lists every configured instance under nvidia-smi vgpu", func(ctx SpecContext) {
			out := nvidiaSMIOnNode(ctx, h, node, "vgpu")
			Expect(strings.Count(out, vgpuTypeName)).To(Equal(p.ExpectedGPUs()*vgpuInstancesPerGPU),
				"one row per instance on every GPU:\n%s", out)
		})

		It("reports the GPU as a vGPU host in nvidia-smi -q", func(ctx SpecContext) {
			out := nvidiaSMIOnNode(ctx, h, node, "-q", "-i", "0")
			Expect(out).To(MatchRegexp(`Virtualization Mode\s+:\s+Host VGPU`))
			Expect(out).To(MatchRegexp(`Host VGPU Mode\s+:\s+SR-IOV`))
		})

		It("details each instance under nvidia-smi vgpu -q", func(ctx SpecContext) {
			out := nvidiaSMIOnNode(ctx, h, node, "vgpu", "-q", "-i", "0")
			Expect(out).To(MatchRegexp(`Active vGPUs\s+:\s+` + strconv.Itoa(vgpuInstancesPerGPU)))
			Expect(out).To(MatchRegexp(`vGPU Name\s+:\s+` + regexp.QuoteMeta(vgpuTypeName)))
			Expect(out).To(MatchRegexp(`Guest Driver Version\s+:\s+N/A`))
		})

		It("lists the supported and creatable types", func(ctx SpecContext) {
			Expect(nvidiaSMIOnNode(ctx, h, node, "vgpu", "-s", "-i", "0")).To(ContainSubstring(vgpuTypeName))
			Expect(nvidiaSMIOnNode(ctx, h, node, "vgpu", "-c", "-i", "0")).To(ContainSubstring(vgpuTypeName),
				"two of ten slots used, so the running type is still creatable")
			verbose := nvidiaSMIOnNode(ctx, h, node, "vgpu", "-s", "-v", "-i", "0")
			Expect(verbose).To(MatchRegexp(`vGPU Type ID\s+:\s+0x1da`))
			Expect(verbose).To(MatchRegexp(`Max Instances\s+:\s+10`))
		})
	})

	Context("guest", Label("vgpu-guest"), Ordered, func() {
		BeforeAll(func(ctx SpecContext) {
			installNvmlMock(ctx, h, p, vgpuGuestValues)
		})

		It("reports a licensed vGPU in nvidia-smi -q", func(ctx SpecContext) {
			out := nvidiaSMIOnNode(ctx, h, node, "-q", "-i", "0")
			Expect(out).To(MatchRegexp(`Virtualization Mode\s+:\s+VGPU`))
			Expect(out).To(MatchRegexp(`Product Name\s+:\s+` + regexp.QuoteMeta(vgpuLicensedProduct)))
			Expect(out).To(MatchRegexp(`License Status\s+:\s+Licensed \(Expiry: Permanent\)`))
		})

		It("is labelled a vGPU by the GPU Operator's GPU Feature Discovery", func(ctx SpecContext) {
			// Fails rather than skips without the operator: the job installs it,
			// and a spec that skips in CI asserts nothing.
			gfdPodRef(ctx, h, node)
			assertions.WaitGFDLabels(ctx, h.Kube, node, map[string]string{
				"nvidia.com/vgpu.present":             "true",
				"nvidia.com/vgpu.host-driver-version": vgpuHostDriver,
				"nvidia.com/vgpu.host-driver-branch":  vgpuHostBranch,
			}, config.ReadyTimeout(), config.PollInterval())
		})
	})
})
