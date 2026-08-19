//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	// multiNodeWorkloadNS is where the scheduling-test pod (`gpu-scheduling-test`)
	// lives. Mock releases live in nvmlMockNamespace ("mokka") like every other
	// scenario — reserving the "default" namespace here for the ordinary workload
	// under test, not the mock DaemonSet.
	multiNodeWorkloadNS = "default"
	a100ReleaseName     = "nvml-mock-a100"
	t4ReleaseName       = "nvml-mock-t4"
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
		h = setupCluster(ctx, "multi-node")
		var err error
		workers, err = h.Cluster.Workers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(workers).To(HaveLen(2), "multi-node scenario requires exactly two Kind workers")
		a100 = loadProfile("a100")
		t4 = loadProfile("t4")
		a100Pod = firstReleasePod(ctx, h, a100ReleaseName)
		t4Pod = firstReleasePod(ctx, h, t4ReleaseName)

		// Setup, not a spec. The scheduling spec below requests
		// nvidia.com/gpu, and the only device plugin in this scenario comes
		// from here — the nvml-mock chart ships none. Deploying it from inside
		// a spec made the scheduling spec depend on that spec being selected,
		// so --focus on it alone failed with "Insufficient nvidia.com/gpu",
		// which reads as a capacity problem rather than skipped setup (#565).
		//
		// deployDevicePlugin carries its own readiness barrier: it waits for
		// the DaemonSet and for the A100 worker to advertise its GPUs, so the
		// scheduling spec has a node to land on. The T4 worker is deliberately
		// left to the spec below, which is the assertion this container makes
		// about heterogeneity.
		deployDevicePlugin(ctx, h, workers[0].Name, a100.ExpectedGPUs())
	})

	It("validates mock files and InfiniBand behavior on both workers", func(ctx SpecContext) {
		assertions.DevicePluginMockFiles(ctx, h.Kube, a100Pod, a100.ExpectedGPUs())
		assertions.DevicePluginMockFiles(ctx, h.Kube, t4Pod, t4.ExpectedGPUs())
		assertions.IBStat(ctx, h.Kube, a100Pod, a100)
		assertions.IBStat(ctx, h.Kube, t4Pod, t4)
	})

	// The A100 worker's count is already established in BeforeAll, so the
	// assertion that carries weight here is the T4 worker reporting its own,
	// different count off the same DaemonSet.
	It("registers the T4 worker's own allocatable GPU count", func(ctx SpecContext) {
		assertions.WaitAllocatableGPU(ctx, h.Kube, workers[1].Name, t4.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
	})

	It("schedules a GPU workload on the heterogeneous fleet", func(ctx SpecContext) {
		manifest := multiNodeSchedulingManifest()
		Expect(h.Kube.Delete(ctx, manifest)).To(Succeed(), "delete previous multi-node scheduling pod")
		Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply multi-node scheduling pod")
		assertions.WaitPodPhase(ctx, h.Kube, multiNodeWorkloadNS, "gpu-scheduling-test", "Running", config.ReadyTimeout(), config.PollInterval())
	})
})

func firstReleasePod(ctx context.Context, h *harness.Harness, releaseName string) kube.PodRef {
	GinkgoHelper()
	selector := "app.kubernetes.io/instance=" + releaseName
	var name string
	Eventually(func() (string, error) {
		n, err := h.Kube.FirstPodName(ctx, nvmlMockNamespace, selector)
		name = n
		return n, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no pod for release %s", releaseName)
	return kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
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
