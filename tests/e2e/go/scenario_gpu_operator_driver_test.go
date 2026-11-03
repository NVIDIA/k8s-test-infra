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
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions/nvidiasmi"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// The GPU Operator's driver pods, whether a ClusterPolicy or an NVIDIADriver
// created them, and the container in them that installs the driver.
const (
	driverPodSelector = "app.kubernetes.io/component=nvidia-driver"
	driverContainer   = "nvidia-driver-ctr"
)

const (
	// upgradeStateLabel is where the operator's upgrade controller tracks a node.
	upgradeStateLabel            = "nvidia.com/gpu-driver-upgrade-state"
	operatorDevicePluginSelector = "app=nvidia-device-plugin-daemonset"
	driverWorkload               = "gpu-operator-driver-workload"
)

// MEP-0004's two stories in the kernel driver mode: the GPU Operator installs
// its driver from NVIDIA's original image, which then loads Mokka's simulated
// kernel modules, and upgrades it through its upgrade controller. The upgrade
// runs last, because it leaves the node on another driver.
var _ = Describe("nvml-mock GPU Operator driver", Label("gpu-operator-driver"), Ordered, func() {
	var (
		h       *harness.Harness
		p       profile.Profile
		node    cluster.Node
		version string
	)

	BeforeAll(func(ctx SpecContext) {
		if !config.KernelDriverMode() {
			Skip("needs the kernel driver mode: E2E_DRIVER_MODE=kernel, on a cluster from `tilt up -- --kernel-mods`")
		}
		h = setupCluster(ctx, "gpu-operator-driver")
		p, _, _ = setupStandaloneProfile(ctx, h, config.SelectedProfileNames()[0])
		node = gpuOperatorTargetNode(ctx, h)
		waitDriverReady(ctx, h, node.Name)
	})

	AfterEach(func(ctx SpecContext) {
		if CurrentSpecReport().Failed() {
			collectDriverDiagnostics(ctx, h, node)
		}
	})

	It("installs the driver from NVIDIA's image on the simulated modules", func(ctx SpecContext) {
		version = driverVersion(ctx, h, waitDriverReady(ctx, h, node.Name))
		Expect(nodeFile(ctx, node, "/sys/module/nvidia/version")).To(Equal(version),
			"the simulated module carries the version of the driver the image installed")

		modules := nodeOutput(ctx, node, "cat", "/proc/modules")
		for _, module := range []string{"nvidia", "nvidia_uvm", "nvidia_modeset", "mokka"} {
			Expect(moduleLoaded(modules, module)).To(BeTrue(), "%s is not loaded", module)
		}
		Expect(strings.Fields(nodeOutput(ctx, node, "ls", "/proc/driver/nvidia/gpus"))).To(HaveLen(p.ExpectedGPUs()),
			"the driver probes every GPU the profile describes")
		Expect(nodeOutput(ctx, node, "ls", "/sys/module/nvidia/drivers")).To(ContainSubstring("platform:nvidia-gpu"),
			"the simulated driver binds platform devices, where the real one binds PCI ones")

		verifyGPUOperatorNodeSetup(ctx, node.Container)
		waitOperatorValidatorRunning(ctx, h)
	})

	It("reports the installed driver's version to a GPU workload", func(ctx SpecContext) {
		ref := runGPUOperatorWorkload(ctx, h, node.Name)
		Expect(workloadDriverVersion(ctx, h, ref)).To(Equal(version),
			"the mock NVML follows the loaded module, not the profile")
	})

	It("upgrades the driver through the operator's upgrade controller", func(ctx SpecContext) {
		target := config.DriverUpgradeVersion()
		policy := clusterPolicy(ctx, h)
		Expect(policy.Spec.Driver.Version).NotTo(Equal(target),
			"E2E_DRIVER_UPGRADE_VERSION must name a driver other than the installed one")

		oldDriver := waitDriverReady(ctx, h, node.Name)
		generation := nodeFile(ctx, node, "/sys/module/mokka/parameters/generation")
		oldWorkload := runGPUDeployment(ctx, h, node.Name)

		By("moving the ClusterPolicy to driver " + target)
		patchClusterPolicyDriverVersion(ctx, h, policy.Metadata.Name, target)

		By("waiting for the upgrade controller to cordon the node")
		Eventually(func() (bool, error) {
			return h.Kube.NodeCordoned(ctx, node.Name)
		}).WithContext(ctx).WithTimeout(config.DriverInstallTimeout()).WithPolling(config.PollInterval()).
			Should(BeTrue(), "the upgrade controller never cordoned %s", node.Name)

		By("waiting for the upgrade to finish on a new driver pod")
		var newDriver driverPod
		Eventually(func(g Gomega) {
			state, _, err := h.Kube.NodeLabel(ctx, node.Name, upgradeStateLabel)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(state).To(Equal("upgrade-done"))

			cordoned, err := h.Kube.NodeCordoned(ctx, node.Name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cordoned).To(BeFalse(), "the upgrade controller uncordons the node when it is done")

			name, uid, err := h.Kube.ReadyPodOnNode(ctx, gpuOperatorNamespace, driverPodSelector, node.Name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(uid).NotTo(Equal(oldDriver.UID), "the driver pod was not replaced")
			newDriver = driverPod{PodRef: driverPodRef(name), UID: uid}
		}).WithContext(ctx).WithTimeout(config.DriverInstallTimeout()).WithPolling(config.PollInterval()).
			Should(Succeed())

		Expect(driverPodImage(ctx, h, newDriver)).To(HaveSuffix("@" + target))
		newVersion := driverVersion(ctx, h, newDriver)
		Expect(newVersion).NotTo(Equal(version), "the new image carries another driver")
		Expect(nodeFile(ctx, node, "/sys/module/nvidia/version")).To(Equal(newVersion),
			"the reloaded module carries the new driver's version")

		By("checking the evicted workload came back on the new driver")
		newWorkload := waitGPUDeploymentPod(ctx, h, node.Name, oldWorkload.UID)
		Expect(workloadDriverVersion(ctx, h, newWorkload.PodRef)).To(Equal(newVersion))
		assertions.WaitAllocatableGPU(ctx, h.Kube, node.Name, p.ExpectedGPUs(),
			config.OperandSettleTimeout(), config.PollInterval())
		// 125b3e6d: after an upgrade the device plugin once looked for the old
		// driver's libraries and crash-looped.
		Eventually(func() error {
			_, _, err := h.Kube.ReadyPodOnNode(ctx, gpuOperatorNamespace, operatorDevicePluginSelector, node.Name)
			return err
		}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
			Should(Succeed(), "no ready device plugin on %s", node.Name)

		By("checking the GPUs outlived the driver")
		Expect(moduleLoaded(nodeOutput(ctx, node, "cat", "/proc/modules"), "mokka")).To(BeTrue())
		Expect(nodeFile(ctx, node, "/sys/module/mokka/parameters/generation")).To(Equal(generation),
			"the node agent described the GPUs again: the mokka module lost them during the upgrade")
	})
})

// driverPod is the GPU Operator's driver pod on a node, with its UID: an
// upgrade replaces it.
type driverPod struct {
	kube.PodRef
	UID string
}

func driverPodRef(name string) kube.PodRef {
	return kube.PodRef{Namespace: gpuOperatorNamespace, Pod: name, Container: driverContainer}
}

// waitDriverReady waits for the GPU Operator's driver pod on node to be ready
// and returns it. In the kernel driver mode that takes the driver container's
// whole installation, NVIDIA's driver build included.
func waitDriverReady(ctx context.Context, h *harness.Harness, node string) driverPod {
	GinkgoHelper()
	var pod driverPod
	Eventually(func() error {
		name, uid, err := h.Kube.ReadyPodOnNode(ctx, gpuOperatorNamespace, driverPodSelector, node)
		pod = driverPod{PodRef: driverPodRef(name), UID: uid}
		return err
	}).WithContext(ctx).WithTimeout(config.DriverInstallTimeout()).WithPolling(config.PollInterval()).
		Should(Succeed(), "the GPU Operator's driver pod on %s never became ready", node)
	return pod
}

// driverVersion is the version of the driver the pod's image installs. The
// operator names the image by digest; the image carries the version.
func driverVersion(ctx context.Context, h *harness.Harness, pod driverPod) string {
	GinkgoHelper()
	res, err := h.Kube.ExecQuiet(ctx, pod.PodRef, "printenv", "DRIVER_VERSION")
	Expect(err).NotTo(HaveOccurred(), "read DRIVER_VERSION in %s: %s", pod.Pod, res.Combined())
	v := strings.TrimSpace(res.Stdout)
	Expect(v).NotTo(BeEmpty(), "%s has no DRIVER_VERSION", pod.Pod)
	return v
}

func driverPodImage(ctx context.Context, h *harness.Harness, pod driverPod) string {
	GinkgoHelper()
	var obj struct {
		Spec struct {
			Containers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"spec"`
	}
	Expect(h.Kube.GetJSON(ctx, &obj, "pod", "-n", pod.Namespace, pod.Pod)).To(Succeed())
	for _, c := range obj.Spec.Containers {
		if c.Name == driverContainer {
			return c.Image
		}
	}
	Fail(fmt.Sprintf("%s has no %s container", pod.Pod, driverContainer))
	return ""
}

func workloadDriverVersion(ctx context.Context, h *harness.Harness, ref kube.PodRef) string {
	GinkgoHelper()
	snap, err := nvidiasmi.SnapshotFromPod(ctx, h.Kube, ref)
	Expect(err).NotTo(HaveOccurred(), "read nvidia-smi -q -x in %s", ref.Pod)
	return snap.DriverVersion()
}

// nodeFile reads a one-line file in the node's Kind container. The kernel the
// driver loads into is the node's, so its modules show there.
func nodeFile(ctx context.Context, node cluster.Node, path string) string {
	GinkgoHelper()
	return strings.TrimSpace(nodeOutput(ctx, node, "cat", path))
}

// moduleLoaded reports whether /proc/modules lists module.
func moduleLoaded(procModules, module string) bool {
	for _, line := range strings.Split(procModules, "\n") {
		if strings.HasPrefix(line, module+" ") {
			return true
		}
	}
	return false
}

type clusterPolicyObj struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Driver struct {
			Version string `json:"version"`
		} `json:"driver"`
	} `json:"spec"`
}

// clusterPolicy returns the cluster's ClusterPolicy; the operator reconciles
// only one.
func clusterPolicy(ctx context.Context, h *harness.Harness) clusterPolicyObj {
	GinkgoHelper()
	var list struct {
		Items []clusterPolicyObj `json:"items"`
	}
	Expect(h.Kube.GetJSON(ctx, &list, "clusterpolicies.nvidia.com")).To(Succeed())
	Expect(list.Items).To(HaveLen(1), "want exactly one ClusterPolicy")
	return list.Items[0]
}

func patchClusterPolicyDriverVersion(ctx context.Context, h *harness.Harness, name, version string) {
	GinkgoHelper()
	patch := fmt.Sprintf(`{"spec":{"driver":{"version":%q}}}`, version)
	out, err := h.Kube.KubectlCombined(ctx, "patch", "clusterpolicies.nvidia.com", name, "--type", "merge", "-p", patch)
	Expect(err).NotTo(HaveOccurred(), "patch ClusterPolicy %s: %s", name, out)
}

// workloadPod is the running pod of the upgrade spec's Deployment, with its UID.
type workloadPod struct {
	kube.PodRef
	UID string
}

// gpuDeployment is a GPU workload pinned to node under a Deployment, so it
// comes back after the upgrade controller evicts it. A bare pod would not: the
// controller force-deletes those (gpuPodDeletion.force).
func gpuDeployment(node string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s
  template:
    metadata:
      labels:
        app: %[1]s
    spec:
      nodeSelector:
        kubernetes.io/hostname: %[3]s
      terminationGracePeriodSeconds: 1
      containers:
      - name: workload
        image: %[4]s
        command: ["/bin/sh", "-c"]
        args: ["trap 'exit 0' TERM; sleep 3600 & wait"]
        resources:
          limits:
            %[5]s: 1
`, driverWorkload, nriWorkloadNS, node, nriWorkloadImage, kube.GPUResourceName))
}

// runGPUDeployment starts gpuDeployment on node, removes it when the spec ends,
// and returns its running pod.
func runGPUDeployment(ctx SpecContext, h *harness.Harness, node string) workloadPod {
	GinkgoHelper()
	manifest := gpuDeployment(node)
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous GPU workload Deployment")
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply GPU workload Deployment")
	DeferCleanup(func(ctx SpecContext) {
		Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete GPU workload Deployment")
	})
	return waitGPUDeploymentPod(ctx, h, node, "")
}

// waitGPUDeploymentPod waits for a ready pod of gpuDeployment on node other
// than the one with replacedUID, and returns it.
func waitGPUDeploymentPod(ctx context.Context, h *harness.Harness, node, replacedUID string) workloadPod {
	GinkgoHelper()
	var pod workloadPod
	Eventually(func(g Gomega) {
		name, uid, err := h.Kube.ReadyPodOnNode(ctx, nriWorkloadNS, "app="+driverWorkload, node)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(uid).NotTo(Equal(replacedUID), "the evicted pod has not been replaced yet")
		pod = workloadPod{PodRef: kube.PodRef{Namespace: nriWorkloadNS, Pod: name}, UID: uid}
	}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
		Should(Succeed(), "no ready GPU workload pod on %s", node)
	return pod
}

// collectDriverDiagnostics saves what explains a failure around the driver: its
// pods with every container's log, k8s-driver-manager's included, the
// ClusterPolicy, and the kernel side of the node.
func collectDriverDiagnostics(ctx context.Context, h *harness.Harness, node cluster.Node) {
	if h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, "gpu-operator-driver")
	c.Kubectl(ctx, "driver-pods.txt", "get", "pods", "-n", gpuOperatorNamespace, "-l", driverPodSelector, "-o", "wide")
	c.Kubectl(ctx, "driver-logs.txt", "logs", "-n", gpuOperatorNamespace, "-l", driverPodSelector,
		"--all-containers", "--prefix", "--tail=-1")
	c.Kubectl(ctx, "clusterpolicy.yaml", "get", "clusterpolicies.nvidia.com", "-o", "yaml")
	if node.Container != "" {
		c.NodeExec(ctx, "kernel-modules.txt", node.Container, "sh", "-c",
			"cat /proc/modules; cat /sys/module/nvidia/version /sys/module/mokka/parameters/generation")
		c.NodeExec(ctx, "kernel-log.txt", node.Container, "sh", "-c", "dmesg | grep -E 'nvidia|mokka' | tail -n 200")
	}
}
