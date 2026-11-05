//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
)

const (
	numaNRTResource    = "noderesourcetopologies.topology.node.k8s.io"
	numaWorkloadName   = "numa-allocation"
	numaWorkloadCPU    = "1"
	numaWorkloadMemory = "64Mi"
)

// The scenario owns no NRT data: the real updater must discover the node and
// publish the resources. Serial keeps kubelet policy changes out of other tests.
var _ = Describe("NUMA topology through the real NFD updater", Label("numa"), Ordered, Serial, func() {
	var (
		h        *harness.Harness
		workers  []cluster.Node
		expected map[string]assertions.NUMAExpectation
		baseline assertions.NodeResourceTopology
		workload []byte
	)
	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "numa")
		DeferCleanup(func(ctx SpecContext) {
			if len(workload) > 0 {
				Expect(h.Kube.Delete(ctx, workload)).To(Succeed())
			}
		})
		var err error
		workers, err = h.Cluster.Workers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(workers).NotTo(BeEmpty(), "NUMA coverage requires a worker")
		expected = numaNodeExpectations(ctx, h, workers)
		installNvmlMock(ctx, h, loadProfile("a100"), string(assets.NUMAValues))
		deployDevicePluginOnWorkers(ctx, h, workers, 8)
		// NFD caches allocatable resources and hardware topology at startup. Start
		// it only after the node's filesystem, managers, and device plugin are ready.
		values, err := assets.WriteTemp("numa-nfd-*.yaml", assets.NUMANFDValues)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Remove, values)
		installNFD(ctx, h, values)
		DeferCleanup(func(ctx SpecContext) {
			Expect(h.Helm.Uninstall(ctx, nfdRelease, nfdNamespace)).To(Succeed())
		})
		assertions.WaitDaemonSetReady(ctx, h.Kube, nfdNamespace, "nfd-node-feature-discovery-topology-updater", config.ReadyTimeout(), config.PollInterval())
	})
	AfterEach(func(ctx SpecContext) { collectNUMADiagnostics(ctx, h) })

	It("publishes both zones and the CPU, memory, and GPU capacities on each worker", func(ctx SpecContext) {
		for _, worker := range workers {
			want := expected[worker.Name]
			waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
				return assertions.NUMATopologyProblems(nrt, want)
			})
		}
	})

	It("accounts for a Guaranteed workload in one NUMA zone", func(ctx SpecContext) {
		node := workers[0].Name
		baseline = waitNUMATopology(ctx, h, node, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMATopologyProblems(nrt, expected[node])
		})
		workload = numaWorkload(numaWorkloadName, node, 1)
		Expect(h.Kube.Apply(ctx, workload)).To(Succeed())

		assertions.WaitPodPhase(ctx, h.Kube, "default", numaWorkloadName, "Running", config.ReadyTimeout(), config.PollInterval())
		request := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(numaWorkloadCPU), corev1.ResourceMemory: resource.MustParse(numaWorkloadMemory), "nvidia.com/gpu": resource.MustParse("1")}
		waitNUMATopology(ctx, h, node, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMAAllocationProblems(baseline, nrt, request)
		})
	})

	It("restores available resources after the workload is deleted", func(ctx SpecContext) {
		Expect(workload).NotTo(BeEmpty(), "the allocation spec must have created a workload")
		Expect(h.Kube.Delete(ctx, workload)).To(Succeed())
		waitNUMATopology(ctx, h, workers[0].Name, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMAAvailabilityProblems(baseline, nrt)
		})
	})

	It("rejects a Guaranteed workload that needs GPUs from both NUMA zones", func(ctx SpecContext) {
		name := "numa-cross-zone"
		node := workers[0].Name
		before := waitNUMATopology(ctx, h, node, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMATopologyProblems(nrt, expected[node])
		})
		// Five GPUs fit on the eight-GPU worker, but cannot fit in either four-GPU
		// zone. Pin the pod so kubelet's Topology Manager makes the admission decision.
		manifest := numaWorkload(name, node, 5)
		DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, manifest)).To(Succeed()) })
		Expect(h.Kube.Apply(ctx, manifest)).To(Succeed())
		assertions.WaitPodPhase(ctx, h.Kube, "default", name, "Failed", config.ReadyTimeout(), config.PollInterval())
		var rejected corev1.Pod
		Expect(h.Kube.GetJSON(ctx, &rejected, "pod", name, "-n", "default")).To(Succeed())
		Expect(rejected.Status.Reason).To(Equal("TopologyAffinityError"), "the pod must fail because its resources cannot fit in one NUMA zone: %s", rejected.Status.Message)
		waitNUMATopology(ctx, h, node, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMAAvailabilityProblems(before, nrt)
		})
	})
})

func numaWorkload(name, node string, gpus int) []byte {
	return pod.Spec{
		Name: name, Namespace: "default", Node: node,
		Image: nriWorkloadImage, CPU: numaWorkloadCPU, Memory: numaWorkloadMemory, GPUs: gpus,
		Command: []string{"sleep", "infinity"},
	}.Render()
}

func numaNodeExpectations(ctx context.Context, h *harness.Harness, workers []cluster.Node) map[string]assertions.NUMAExpectation {
	GinkgoHelper()
	out := map[string]assertions.NUMAExpectation{}
	for _, worker := range workers {
		var node struct {
			Status struct {
				Capacity corev1.ResourceList `json:"capacity"`
			} `json:"status"`
		}
		Expect(h.Kube.GetJSON(ctx, &node, "node", worker.Name)).To(Succeed())
		cpus := node.Status.Capacity.Cpu().Value()
		Expect(cpus).To(BeNumerically(">=", 2), "two NUMA zones need at least two real CPUs")
		out[worker.Name] = assertions.NUMAExpectation{Node: worker.Name, CPUs: cpus, MemoryBytes: node.Status.Capacity.Memory().Value(), GPUsPerZone: 4}
	}
	return out
}

func waitNUMATopology(ctx context.Context, h *harness.Harness, node string, check func(assertions.NodeResourceTopology) []string) assertions.NodeResourceTopology {
	GinkgoHelper()
	var nrt assertions.NodeResourceTopology
	Eventually(func() ([]string, error) {
		var current assertions.NodeResourceTopology
		if err := h.Kube.GetJSON(ctx, &current, numaNRTResource, node); err != nil {
			return nil, err
		}
		nrt = current
		return check(current), nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(BeEmpty(), "NRT %s did not reach the expected state", node)
	return nrt
}

func collectNUMADiagnostics(ctx context.Context, h *harness.Harness) {
	if !CurrentSpecReport().Failed() || h == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, "numa")
	c.Kubectl(ctx, "nrt.yaml", "get", numaNRTResource, "-o", "yaml")
	c.PodLogs(ctx, "topology-updater", nfdNamespace, "role=topology-updater", 200)
	c.PodLogs(ctx, "device-plugin", devicePluginNamespace, devicePluginSelector, 100)
	workers, err := h.Cluster.Workers(ctx)
	if err != nil {
		return
	} // Common diagnostics still retain the API failure.
	for _, node := range workers {
		c.NodeExec(ctx, node.Name+"-kubelet.txt", node.Container, "journalctl", "-u", "kubelet", "--no-pager", "-n", "100")
		c.NodeExec(ctx, node.Name+"-numa.txt", node.Container, "sh", "-c", "cat /sys/devices/system/node/online /var/lib/kubelet/config.yaml; cat /var/lib/kubelet/cpu_manager_state /var/lib/kubelet/memory_manager_state")
	}
}
