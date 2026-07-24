//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

const (
	managedDriverClusterName     = "nvml-mock-op-drv"
	managedDriverKmodClusterName = "nvml-mock-op-drv-kmod"
	nvidiaDriverSelector         = "app.kubernetes.io/component=nvidia-driver"
)

// The managed-driver lane runs the GPU Operator's full containerized-driver
// lifecycle against the mock-driver image (driver.enabled=true): DaemonSet
// rendering + image-tag resolution, the k8s-driver-manager init flow, the
// startup-probe -> .driver-ctr-ready handshake, and the validator's
// operator-managed branch. It is pinned to the operator version whose
// driver-container contract is vendored under tests/e2e/contract/.
//
// It runs on its OWN Kind cluster (the driver DaemonSet owns /run/nvidia/driver
// via a Bidirectional rbind, so it must never share a node with the
// driver-disabled or hostDriver lanes) and on a single fixed profile (a100/2).
var _ = Describe("nvml-mock GPU Operator managed driver", Label("gpu-operator-driver"), Ordered, func() {
	var (
		h    *harness.Harness
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		buildMockDriverImage(ctx)
		h = setupCluster(ctx, managedDriverClusterName, assets.KindGPUOperatorConfig, "gpu-operator-driver")
		cp, err := h.Cluster.ControlPlane(ctx)
		Expect(err).NotTo(HaveOccurred())
		node = cp.Name
		installNVIDIAContainerToolkit(ctx, h, cp)
		configureNVIDIARuntimeCDI(ctx, h, cp)
		loadMockDriverImage(ctx, h, node)
		installNvmlMockManagedDriver(ctx, h, managedDriverProfile, managedDriverGPUs)
		DeferCleanup(func(ctx SpecContext) { collectManagedDriverOnFailure(ctx, h, "gpu-operator-driver") })
	})

	It("runs the operator-managed driver DaemonSet and publishes GPUs", Label("device-plugin"), func(ctx SpecContext) {
		installGPUOperatorManagedDriver(ctx, h, assets.GPUOperatorValues, assets.GPUOperatorDriverValues)
		waitDriverPodRunning(ctx, h)
		waitOperatorValidatorRunning(ctx, h)
		assertions.WaitAllocatableGPU(ctx, h.Kube, node, managedDriverGPUs, config.ReadyTimeout(), config.PollInterval())
	})

	It("satisfies the driver-container contract inside the driver pod", func(ctx SpecContext) {
		assertManagedDriverContract(ctx, h)
	})
})

// The kmod lane is a SEPARATE cluster because the stub `nvidia` module is
// node-global and, once loaded, persists for the node's lifetime (and
// k8s-driver-manager may unload it on a driver-pod restart). Isolating it keeps
// the kmod-off lane's namespace fakes uncontaminated.
var _ = Describe("nvml-mock GPU Operator managed driver (MOCK_KMOD)", Label("gpu-operator-driver-kmod"), Ordered, func() {
	var (
		h    *harness.Harness
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		buildMockDriverImage(ctx)
		h = setupCluster(ctx, managedDriverKmodClusterName, assets.KindGPUOperatorConfig, "gpu-operator-driver-kmod")
		cp, err := h.Cluster.ControlPlane(ctx)
		Expect(err).NotTo(HaveOccurred())
		node = cp.Name
		installNVIDIAContainerToolkit(ctx, h, cp)
		configureNVIDIARuntimeCDI(ctx, h, cp)
		loadMockDriverImage(ctx, h, node)
		prebuildAndStageStubKmod(ctx, node, managedDriverProfileVersion())
		installNvmlMockManagedDriver(ctx, h, managedDriverProfile, managedDriverGPUs)
		DeferCleanup(func(ctx SpecContext) { collectManagedDriverOnFailure(ctx, h, "gpu-operator-driver-kmod") })
	})

	It("loads the prebuilt stub module and publishes GPUs", Label("device-plugin"), func(ctx SpecContext) {
		installGPUOperatorManagedDriver(ctx, h,
			assets.GPUOperatorValues, assets.GPUOperatorDriverValues, assets.GPUOperatorDriverKmodValues)
		waitDriverPodRunning(ctx, h)
		waitOperatorValidatorRunning(ctx, h)
		assertions.WaitAllocatableGPU(ctx, h.Kube, node, managedDriverGPUs, config.ReadyTimeout(), config.PollInterval())
	})

	It("exposes real kernel-global /proc and /sys on the node", func(ctx SpecContext) {
		assertKmodNodeVisible(ctx, node)
	})

	It("exposes real kernel-global /proc and /sys inside an ordinary pod", func(ctx SpecContext) {
		assertKmodPodVisible(ctx, h)
	})
})

// waitDriverPodRunning waits for the operator-managed driver pod to reach
// Running, keyed off the STABLE component label (app.kubernetes.io/component=
// nvidia-driver) rather than the DaemonSet name, which the operator may suffix
// with the driver version / OS tag at render time.
func waitDriverPodRunning(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	var pod string
	Eventually(func() (string, error) {
		p, err := h.Kube.FirstPodName(ctx, gpuOperatorNamespace, nvidiaDriverSelector)
		pod = p
		return p, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "nvidia-driver daemonset pod not found")
	assertions.WaitPodPhase(ctx, h.Kube, gpuOperatorNamespace, pod, "Running", config.ReadyTimeout(), config.PollInterval())
}

// firstDriverPod returns the running driver pod name (by the stable component
// label).
func firstDriverPod(ctx context.Context, h *harness.Harness) string {
	GinkgoHelper()
	var pod string
	Eventually(func() (string, error) {
		p, err := h.Kube.FirstPodName(ctx, gpuOperatorNamespace, nvidiaDriverSelector)
		pod = p
		return p, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "nvidia-driver daemonset pod not found")
	return pod
}

// assertManagedDriverContract verifies the driver pod satisfies the operator's
// contract: the startup-probe sentinel exists and nvidia-smi runs from the
// rbind-exposed driver root inside the driver container.
func assertManagedDriverContract(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	driverPod := firstDriverPod(ctx, h)
	ref := kube.PodRef{Namespace: gpuOperatorNamespace, Pod: driverPod, Container: "nvidia-driver-ctr"}
	By("checking /sys/module/nvidia/refcnt exists in the driver container")
	_, err := h.Kube.ExecSh(ctx, ref, "test -f /sys/module/nvidia/refcnt")
	Expect(err).NotTo(HaveOccurred(), "driver container missing /sys/module/nvidia/refcnt")

	By("checking nvidia-smi runs from the driver root")
	_, err = h.Kube.ExecSh(ctx, ref, "/run/nvidia/driver/usr/bin/nvidia-smi")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi failed in driver container")
}

// assertKmodNodeVisible checks the node's REAL /proc/driver/nvidia and
// /sys/module/nvidia (created by the loaded stub module, not a namespace fake).
func assertKmodNodeVisible(ctx context.Context, node string) {
	GinkgoHelper()
	out, err := dockerExecOut(ctx, node, "cat", "/proc/driver/nvidia/version")
	Expect(err).NotTo(HaveOccurred(), "node /proc/driver/nvidia/version")
	Expect(out).To(ContainSubstring("NVRM version"), "node NVRM version string")
	_, err = dockerExecOut(ctx, node, "cat", "/sys/module/nvidia/refcnt")
	Expect(err).NotTo(HaveOccurred(), "node /sys/module/nvidia/refcnt")
}

// assertKmodPodVisible schedules an ordinary pod (no GPU request, no mounts)
// and asserts it sees the kernel-global /proc/driver/nvidia -- the property
// only a real module load provides.
func assertKmodPodVisible(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	manifest := kmodCheckPodManifest()
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous kmod-check pod")
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply kmod-check pod")
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) })
	assertions.WaitPodPhase(ctx, h.Kube, "default", "kmod-check", "Succeeded", config.ReadyTimeout(), config.PollInterval())
	out, err := h.Kube.KubectlCombined(ctx, "logs", "-n", "default", "kmod-check")
	Expect(err).NotTo(HaveOccurred(), "kmod-check pod logs")
	Expect(out).To(ContainSubstring("NVRM version"), "ordinary pod did not see kernel-global /proc/driver/nvidia")
}

// kmodCheckPodManifest is a plain pod that reads the kernel-global procfs and
// exits. No nvidia.com/gpu request, no hostPath/mock volumes.
func kmodCheckPodManifest() []byte {
	return []byte(`apiVersion: v1
kind: Pod
metadata:
  name: kmod-check
  namespace: default
spec:
  restartPolicy: Never
  containers:
    - name: check
      image: busybox:1.36
      command: ["sh", "-c", "cat /proc/driver/nvidia/version && cat /sys/module/nvidia/refcnt"]
`)
}

// dockerExecOut runs argv in a Kind node container and returns combined output.
func dockerExecOut(ctx context.Context, node string, args ...string) (string, error) {
	all := append([]string{"exec", node}, args...)
	res, err := runner.Run(ctx, "docker", all...)
	return res.Combined(), err
}

func collectManagedDriverOnFailure(ctx context.Context, h *harness.Harness, sub string) {
	if !CurrentSpecReport().Failed() || h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, sub)
	c.NvmlMockNamespace = nvmlMockNamespace
	c.Common(ctx)
	c.Kubectl(ctx, "gpu-operator-pods.txt", "get", "pods", "-n", gpuOperatorNamespace, "-o", "wide")
	c.Kubectl(ctx, "driver-daemonset-describe.txt", "describe", "daemonset", "-n", gpuOperatorNamespace, "nvidia-driver-daemonset")
	c.Kubectl(ctx, "driver-pod-logs.txt", "logs", "-n", gpuOperatorNamespace, "-l", nvidiaDriverSelector, "--all-containers", "--tail=200")
	c.Kubectl(ctx, "clusterpolicy.yaml", "get", "clusterpolicy", "-o", "yaml")
}
