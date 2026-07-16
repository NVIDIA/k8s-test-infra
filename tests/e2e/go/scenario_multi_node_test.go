//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	multiNodeClusterName = "gpu-fleet"
	multiNodeNamespace   = "default"
	a100ReleaseName      = "nvml-mock-a100"
	t4ReleaseName        = "nvml-mock-t4"
)

var _ = Describe("nvml-mock multi-node", Label("multi-node"), Ordered, func() {
	var (
		h       *harness.Harness
		workers []cluster.Node
		a100    profile.Profile
		t4      profile.Profile
		a100Pod kube.PodRef
		t4Pod   kube.PodRef
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, multiNodeClusterName, assets.KindMultiNodeConfig, "multi-node")
		var err error
		workers, err = h.Cluster.Workers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(workers).To(HaveLen(2), "multi-node scenario requires exactly two Kind workers")
		for _, node := range workers {
			installNVIDIAContainerToolkit(ctx, h, node)
			Expect(dockerExec(ctx, node.Name, "systemctl", "restart", "containerd")).To(Succeed(), "restart containerd in %s", node.Name)
			assertions.WaitNodeReady(ctx, h.Kube, node.Name, config.ReadyTimeout(), config.PollInterval())
		}
		a100 = loadProfile("a100")
		t4 = loadProfile("t4")
		installProfileOnNode(ctx, h, a100ReleaseName, "a100", "a100")
		installProfileOnNode(ctx, h, t4ReleaseName, "t4", "t4")
		a100Pod = firstReleasePod(ctx, h, a100ReleaseName)
		t4Pod = firstReleasePod(ctx, h, t4ReleaseName)
	})

	It("validates mock files and InfiniBand behavior on both workers", func(ctx SpecContext) {
		assertions.DevicePluginMockFiles(ctx, h.Kube, a100Pod, a100.ExpectedGPUs())
		assertions.DevicePluginMockFiles(ctx, h.Kube, t4Pod, t4.ExpectedGPUs())
		assertions.IBStat(ctx, h.Kube, a100Pod, a100)
		assertions.IBStat(ctx, h.Kube, t4Pod, t4)
	})

	It("registers heterogeneous allocatable GPUs via the device plugin", func(ctx SpecContext) {
		deployDevicePlugin(ctx, h, workers[0].Name, a100.ExpectedGPUs())
		assertions.WaitAllocatableGPU(ctx, h.Kube, workers[1].Name, t4.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
	})

	It("schedules a GPU workload on the heterogeneous fleet", func(ctx SpecContext) {
		manifest := multiNodeSchedulingManifest()
		Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous multi-node scheduling pod")
		Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply multi-node scheduling pod")
		assertions.WaitPodPhase(ctx, h.Kube, multiNodeNamespace, "gpu-scheduling-test", "Running", config.ReadyTimeout(), config.PollInterval())
	})
})

func installProfileOnNode(ctx context.Context, h *harness.Harness, releaseName, profileName, nodeProfile string) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:      releaseName,
		Chart:     chartDir(),
		Namespace: multiNodeNamespace,
		Set: map[string]string{
			"gpu.profile":                    profileName,
			"image.repository":               repo,
			"image.tag":                      tag,
			"nodeSelector.nvml-mock/profile": nodeProfile,
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	})).To(Succeed(), "install %s on %s worker", releaseName, nodeProfile)
}

func firstReleasePod(ctx context.Context, h *harness.Harness, releaseName string) kube.PodRef {
	GinkgoHelper()
	selector := fmt.Sprintf("app.kubernetes.io/instance=%s", releaseName)
	var name string
	Eventually(func() (string, error) {
		n, err := h.Kube.FirstPodName(ctx, multiNodeNamespace, selector)
		name = n
		return n, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no pod for release %s", releaseName)
	return kube.PodRef{Namespace: multiNodeNamespace, Pod: name}
}

func multiNodeSchedulingManifest() []byte {
	return []byte(`apiVersion: v1
kind: Pod
metadata:
  name: gpu-scheduling-test
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: busybox:1.36
      command: ["sh", "-c", "echo scheduled-on=$(hostname); sleep 5"]
      resources:
        limits:
          nvidia.com/gpu: "1"
`)
}
