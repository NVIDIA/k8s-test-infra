//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
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
	gfdVGPUName         = "nvidia-gfd-mock-vgpu"
	gfdVGPUSelector     = "name=nvidia-gfd-mock-vgpu"
	gfdFeatureFile      = "/etc/kubernetes/node-feature-discovery/features.d/gfd"
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
// nvidia-smi plus GPU Feature Discovery in the guest, whose vgpu.* labels come
// from the PCI capability rather than from NVML.
var _ = Describe("nvml-mock vGPU", Label("vgpu"), Ordered, func() {
	var (
		h    *harness.Harness
		node string
		gpus int
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "vgpu")
		node = migTargetNode(ctx, h)
		gpus = loadProfile(vgpuProfile).ExpectedGPUs()
	})

	AfterAll(func(ctx SpecContext) {
		installVGPUChart(ctx, h, "")
	})

	Context("host", Label("vgpu-host"), Ordered, func() {
		BeforeAll(func(ctx SpecContext) {
			installVGPUChart(ctx, h, vgpuHostValues)
		})

		It("lists every configured instance under nvidia-smi vgpu", func(ctx SpecContext) {
			out := nvidiaSMIOnNode(ctx, h, node, "vgpu")
			Expect(strings.Count(out, vgpuTypeName)).To(Equal(gpus*vgpuInstancesPerGPU),
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
			Expect(out).To(MatchRegexp(`Guest Driver Version\s+:\s+\S+`))
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
			installVGPUChart(ctx, h, vgpuGuestValues)
		})

		It("reports a licensed vGPU in nvidia-smi -q", func(ctx SpecContext) {
			out := nvidiaSMIOnNode(ctx, h, node, "-q", "-i", "0")
			Expect(out).To(MatchRegexp(`Virtualization Mode\s+:\s+VGPU`))
			Expect(out).To(MatchRegexp(`Product Name\s+:\s+` + regexp.QuoteMeta(vgpuLicensedProduct)))
			Expect(out).To(MatchRegexp(`License Status\s+:\s+Licensed`))
		})

		It("is labelled a vGPU by GPU Feature Discovery", func(ctx SpecContext) {
			if !config.RunNGCSpecs() {
				Skip("set E2E_RUN_NGC=true to run the nvcr.io-backed GFD spec; see #446")
			}
			Expect(h.Kube.Apply(ctx, assets.GFDVGPUManifest)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				Expect(h.Kube.Delete(ctx, assets.GFDVGPUManifest)).To(Succeed())
			})
			assertions.WaitDaemonSetReady(ctx, h.Kube, gfdNamespace, gfdVGPUName, config.ReadyTimeout(), config.PollInterval())
			target := podOnNode(ctx, h, gfdNamespace, gfdVGPUSelector, node)
			Eventually(func() (string, error) {
				res, err := h.Kube.ExecQuiet(ctx, target, "cat", gfdFeatureFile)
				return res.Stdout, err
			}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(And(
				ContainSubstring("nvidia.com/vgpu.present=true"),
				ContainSubstring("nvidia.com/vgpu.host-driver-version="+vgpuHostDriver),
				ContainSubstring("nvidia.com/vgpu.host-driver-branch="+vgpuHostBranch),
			))
		})
	})
})

// installVGPUChart reinstalls nvml-mock on the vGPU profile with values
// layered on top; empty values restores the bare-metal install.
func installVGPUChart(ctx context.Context, h *harness.Harness, values string) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	p := loadProfile(vgpuProfile)
	rel := helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: map[string]string{
			"gpu.count":        strconv.Itoa(p.ExpectedGPUs()),
			"gpu.profile":      p.Name,
			"image.repository": repo,
			"image.tag":        tag,
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
	if values != "" {
		path := filepath.Join(GinkgoT().TempDir(), "vgpu-values.yaml")
		Expect(os.WriteFile(path, []byte(values), 0o600)).To(Succeed())
		rel.ValuesFiles = []string{path}
	}
	By(fmt.Sprintf("helm upgrade --install nvml-mock (profile=%s, vgpu values=%t)", p.Name, values != ""))
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
}

// nvidiaSMIOnNode runs nvidia-smi in the nvml-mock pod on node and returns its
// output; a non-zero exit fails the spec.
func nvidiaSMIOnNode(ctx context.Context, h *harness.Harness, node string, args ...string) string {
	GinkgoHelper()
	target := nvmlPodOnNode(ctx, h, node)
	By("nvidia-smi " + strings.Join(args, " "))
	res, err := h.Kube.ExecQuiet(ctx, target, append([]string{"nvidia-smi"}, args...)...)
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi %v in %s: %s", args, target.Pod, res.Combined())
	return res.Combined()
}

// podOnNode returns the running pod matching selector on node.
func podOnNode(ctx context.Context, h *harness.Harness, ns, selector, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		n, err := h.Kube.RunningPodOnNode(ctx, ns, selector, node)
		name = n
		return n, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running %s pod on node %s", selector, node)
	return kube.PodRef{Namespace: ns, Pod: name}
}
