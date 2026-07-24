//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
)

const (
	hostDriverClusterName = "nvml-mock-op-host"
)

// hostResidueProbe lists any nvml-mock host-driver masquerade files that remain
// on the node. After `helm uninstall`, this must print nothing -- the manifest
// cleanup should remove exactly what setup installed. Kept as a var so the unit
// test can pin the exact paths without executing the probe.
var hostResiduePaths = []string{
	"/usr/bin/nvidia-smi",
	"/usr/lib64/libnvidia-ml.so.1",
	"/etc/ld.so.conf.d/00-nvml-mock.conf",
	"/config",
}

// The host-driver lane installs nvml-mock with hostDriver.enabled=true, which
// puts nvidia-smi and the mock libraries at the node's STANDARD paths so the
// operator validator takes its preinstalled host-driver branch
// (IS_HOST_DRIVER=true) with zero driver-root env overrides. It runs on its own
// cluster (it mutates the node root) and a single fixed profile.
var _ = Describe("nvml-mock GPU Operator host driver", Label("gpu-operator-hostdriver"), Ordered, func() {
	var (
		h    *harness.Harness
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, hostDriverClusterName, assets.KindGPUOperatorConfig, "gpu-operator-hostdriver")
		cp, err := h.Cluster.ControlPlane(ctx)
		Expect(err).NotTo(HaveOccurred())
		node = cp.Name
		installNVIDIAContainerToolkit(ctx, h, cp)
		configureNVIDIARuntimeCDI(ctx, h, cp)
		installNvmlMockHostDriver(ctx, h, managedDriverProfile, managedDriverGPUs)
		DeferCleanup(func(ctx SpecContext) { collectHostDriverOnFailure(ctx, h) })
	})

	It("masquerades nvidia-smi and mock libs at the node's standard paths", func(ctx SpecContext) {
		assertHostMasqueradeInstalled(ctx, node)
	})

	It("takes the operator validator's host-driver branch and publishes GPUs", Label("device-plugin"), func(ctx SpecContext) {
		installGPUOperatorHostDriver(ctx, h)
		waitOperatorValidatorRunning(ctx, h)
		assertions.WaitAllocatableGPU(ctx, h.Kube, node, managedDriverGPUs, config.ReadyTimeout(), config.PollInterval())
	})

	It("leaves no host residue after helm uninstall", func(ctx SpecContext) {
		assertHostResidueRemovedAfterUninstall(ctx, h, node)
	})
})

// installNvmlMockHostDriver installs nvml-mock with the host-driver masquerade
// enabled (and the driver symlink left at its default; hostDriver mode does not
// use it).
func installNvmlMockHostDriver(ctx context.Context, h *harness.Harness, prof string, count int) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	By("helm upgrade --install nvml-mock (hostDriver.enabled=true)")
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: map[string]string{
			"gpu.profile":        prof,
			"gpu.count":          fmt.Sprintf("%d", count),
			"image.repository":   repo,
			"image.tag":          tag,
			"hostDriver.enabled": "true",
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	})).To(Succeed(), "install nvml-mock hostDriver mode")
}

func installGPUOperatorHostDriver(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Helm.RepoAdd(ctx, "nvidia", "https://helm.ngc.nvidia.com/nvidia")).To(Succeed(), "add NVIDIA Helm repo")
	Expect(h.Helm.RepoUpdate(ctx)).To(Succeed(), "update Helm repos")
	paths, cleanup := writeOperatorValuesFiles(assets.GPUOperatorValues, assets.GPUOperatorHostDriverValues)
	DeferCleanup(cleanup)
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:            gpuOperatorRelease,
		Chart:           gpuOperatorChart,
		Namespace:       gpuOperatorNamespace,
		CreateNamespace: true,
		ValuesFiles:     paths,
		Wait:            true,
		Timeout:         config.HelmTimeout(),
	})).To(Succeed(), "install GPU Operator (host driver)")
}

// assertHostMasqueradeInstalled checks nvidia-smi resolves at the node's
// STANDARD path and libnvidia-ml is registered in the host ldcache (the zero-
// config properties the host-driver branch depends on).
func assertHostMasqueradeInstalled(ctx context.Context, node string) {
	GinkgoHelper()
	_, err := dockerExecOut(ctx, node, "test", "-f", "/usr/bin/nvidia-smi")
	Expect(err).NotTo(HaveOccurred(), "host /usr/bin/nvidia-smi missing")
	out, err := dockerExecOut(ctx, node, "sh", "-c", "ldconfig -p | grep libnvidia-ml")
	Expect(err).NotTo(HaveOccurred(), "libnvidia-ml not in host ldcache")
	Expect(out).To(ContainSubstring("libnvidia-ml.so"), "libnvidia-ml not registered in ldcache")
}

// assertHostResidueRemovedAfterUninstall uninstalls the nvml-mock release,
// waits for the preStop cleanup to drain, and asserts none of the masquerade
// paths remain on the node.
func assertHostResidueRemovedAfterUninstall(ctx context.Context, h *harness.Harness, node string) {
	GinkgoHelper()
	By("helm uninstall nvml-mock")
	Expect(h.Helm.Uninstall(ctx, "nvml-mock", nvmlMockNamespace)).To(Succeed(), "uninstall nvml-mock")

	By("waiting for the nvml-mock DaemonSet pods to terminate (preStop cleanup)")
	Eventually(func() (string, error) {
		return h.Kube.KubectlCombined(ctx, "get", "pods", "-n", nvmlMockNamespace, "-l", nvmlMockSelector, "--no-headers")
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(Or(BeEmpty(), ContainSubstring("No resources found")), "nvml-mock pods still present after uninstall")

	By("asserting no host-driver masquerade residue remains on the node")
	out, err := dockerExecOut(ctx, node, "sh", "-c", hostResidueCheckCmd())
	Expect(err).NotTo(HaveOccurred(), "residue check command")
	Expect(strings.TrimSpace(out)).To(BeEmpty(), "host driver masquerade left residue:\n%s", out)
}

// hostResidueCheckCmd prints every masquerade path that still exists (empty
// output == clean uninstall). `ls -d` on a missing path prints to stderr, which
// the check discards, so only survivors reach stdout.
func hostResidueCheckCmd() string {
	var sb strings.Builder
	for _, p := range hostResiduePaths {
		fmt.Fprintf(&sb, "ls -d %s 2>/dev/null; ", p)
	}
	return sb.String()
}

func collectHostDriverOnFailure(ctx context.Context, h *harness.Harness) {
	if !CurrentSpecReport().Failed() || h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, "gpu-operator-hostdriver")
	c.NvmlMockNamespace = nvmlMockNamespace
	c.Common(ctx)
	c.Kubectl(ctx, "gpu-operator-pods.txt", "get", "pods", "-n", gpuOperatorNamespace, "-o", "wide")
	c.Kubectl(ctx, "validator-describe.txt", "describe", "pod", "-n", gpuOperatorNamespace, "-l", "app=nvidia-operator-validator")
}
