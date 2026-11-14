//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

func singleZoneNUMAProfile(raw string) (string, error) {
	var profile map[string]any
	if err := yaml.Unmarshal([]byte(raw), &profile); err != nil {
		return "", fmt.Errorf("decode profile: %w", err)
	}
	topology, ok := profile["pcie_topology"].(map[string]any)
	if !ok {
		return "", errors.New("profile has no PCI topology")
	}
	roots, ok := topology["root_complexes"].([]any)
	if !ok || len(roots) != 2 {
		return "", errors.New("expected the A100 fixture's two root complexes")
	}
	for _, root := range roots {
		rootConfig, ok := root.(map[string]any)
		if !ok {
			return "", errors.New("invalid PCI root complex")
		}
		rootConfig["numa_node"] = 0
	}
	data, err := yaml.Marshal(profile)
	return string(data), err
}

type numaAgentIdentity struct {
	UID      types.UID
	Restarts int32
}

func numaAgentIdentities(ctx context.Context, h *harness.Harness) map[string]numaAgentIdentity {
	GinkgoHelper()
	var pods corev1.PodList
	Expect(h.Kube.GetJSON(ctx, &pods, "pods", "-n", nvmlMockNamespace, "-l", nvmlMockSelector)).To(Succeed())
	out := map[string]numaAgentIdentity{}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		// With NRI, node-agent is a restartable init container.
		statuses := append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...)
		for _, status := range statuses {
			if status.Name == nodeAgentContainer {
				Expect(out).NotTo(HaveKey(pod.Spec.NodeName), "one Mokka pod is expected per worker")
				out[pod.Spec.NodeName] = numaAgentIdentity{UID: pod.UID, Restarts: status.RestartCount}
			}
		}
	}
	Expect(out).NotTo(BeEmpty(), "the test must observe the running node-agent")
	return out
}

func numaHostFile(ctx context.Context, node cluster.Node, path string) (string, error) {
	result, err := runner.RunQuiet(ctx, "docker", "exec", node.Container, "cat", path)
	return strings.TrimSpace(result.Stdout), err
}

func patchNUMAProfile(ctx context.Context, h *harness.Harness, raw string) {
	GinkgoHelper()
	patch, err := json.Marshal(map[string]any{"data": map[string]string{"config.yaml": raw}})
	Expect(err).NotTo(HaveOccurred())
	_, err = runner.RunInput(ctx, string(patch), "kubectl", "--context", h.Kube.Context,
		"patch", "configmap", "nvml-mock-config", "-n", nvmlMockNamespace,
		"--type=merge", "--patch-file=/dev/stdin")
	Expect(err).NotTo(HaveOccurred(), "change only the live profile, without a Helm upgrade or Mokka rollout")
}

func waitNUMALayout(ctx SpecContext, h *harness.Harness, workers []cluster.Node, online string, previous map[string]string) map[string]string {
	GinkgoHelper()
	markers := map[string]string{}
	// Allow projected ConfigMap propagation before the agent restarts kubelet.
	timeout := 2 * config.ReadyTimeout()
	for _, worker := range workers {
		Eventually(func(g Gomega) {
			got, err := numaHostFile(ctx, worker, "/sys/devices/system/node/online")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(online), worker.Name)
			applied, err := numaHostFile(ctx, worker, "/run/mokka/numa/.applied")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(applied).NotTo(BeEmpty())
			if previous != nil {
				g.Expect(applied).NotTo(Equal(previous[worker.Name]), "the previous layout must be replaced")
			}
			desired, err := numaHostFile(ctx, worker, "/var/lib/nvml-mock/numa/desired")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(applied).To(Equal(desired), "the applied layout must match the new plan")
			ready, err := h.Kube.NodeReady(ctx, worker.Name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ready).To(BeTrue())
			markers[worker.Name] = applied
		}).WithContext(ctx).WithTimeout(timeout).WithPolling(config.PollInterval()).Should(Succeed())
	}
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	return markers
}

func refreshNUMAConsumers(ctx SpecContext, h *harness.Harness, workers []cluster.Node) {
	GinkgoHelper()
	// Both consumers cache hardware topology. Refresh them only after the live
	// agent has installed the layout, so they cannot report the previous view.
	deployDevicePluginOnWorkers(ctx, h, workers, 8)
	Expect(h.Kube.DeletePodsByLabel(ctx, nfdNamespace, "role=topology-updater")).To(Succeed())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nfdNamespace, "nfd-node-feature-discovery-topology-updater", config.ReadyTimeout(), config.PollInterval())
}

func checkNUMAFiveGPUAdmission(ctx SpecContext, h *harness.Harness, want assertions.NUMAExpectation, name string, admit bool) {
	GinkgoHelper()
	before := waitNUMATopology(ctx, h, want.Node, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMATopologyProblems(nrt, want)
	})
	manifest := numaWorkload(name, want.Node, 5)
	DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, manifest)).To(Succeed()) })
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed())
	if admit {
		Eventually(func(g Gomega) {
			var workload corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &workload, "pod", name, "-n", "default")).To(Succeed())
			g.Expect(workload.Status.Phase).To(Equal(corev1.PodRunning))
			g.Expect(workload.Status.Conditions).To(ContainElement(And(
				HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
		request := corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse(numaWorkloadCPU), corev1.ResourceMemory: resource.MustParse(numaWorkloadMemory),
			"nvidia.com/gpu": resource.MustParse("5"),
		}
		waitNUMATopology(ctx, h, want.Node, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMAAllocationProblems(before, nrt, request)
		})
	} else {
		assertions.WaitPodPhase(ctx, h.Kube, "default", name, "Failed", config.ReadyTimeout(), config.PollInterval())
		var rejected corev1.Pod
		Expect(h.Kube.GetJSON(ctx, &rejected, "pod", name, "-n", "default")).To(Succeed())
		Expect(rejected.Status.Reason).To(Equal("TopologyAffinityError"), rejected.Status.Message)
		waitNUMATopology(ctx, h, want.Node, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMAAvailabilityProblems(before, nrt)
		})
	}
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed())
	waitNUMATopology(ctx, h, want.Node, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMAAvailabilityProblems(before, nrt)
	})
}

func checkNUMALiveReconfiguration(ctx SpecContext, h *harness.Harness, workers []cluster.Node, expected map[string]assertions.NUMAExpectation) {
	GinkgoHelper()
	original, err := h.Kube.ConfigMapData(ctx, nvmlMockNamespace, "nvml-mock-config", "config.yaml")
	Expect(err).NotTo(HaveOccurred())
	single, err := singleZoneNUMAProfile(original)
	Expect(err).NotTo(HaveOccurred())
	agents := numaAgentIdentities(ctx, h)
	Expect(agents).To(HaveLen(len(workers)))
	markers := waitNUMALayout(ctx, h, workers, "0-1", nil)
	checkNUMAFiveGPUAdmission(ctx, h, expected[workers[0].Name], "numa-before-update", false)
	// Register restoration before changing the profile, including when an
	// assertion fails during the transition or workload admission.
	restored := false
	DeferCleanup(func(ctx SpecContext) {
		if !restored {
			patchNUMAProfile(ctx, h, original)
			waitNUMALayout(ctx, h, workers, "0-1", nil)
			refreshNUMAConsumers(ctx, h, workers)
		}
	})
	for _, phase := range []struct {
		profile string
		online  string
		zones   int
		name    string
	}{
		{single, "0", 1, "numa-single-zone"},
		{original, "0-1", 2, "numa-restored-zones"},
	} {
		By(fmt.Sprintf("changing the running agent's profile to %d NUMA zones", phase.zones))
		patchNUMAProfile(ctx, h, phase.profile)
		markers = waitNUMALayout(ctx, h, workers, phase.online, markers)
		Expect(numaAgentIdentities(ctx, h)).To(Equal(agents), "the existing node-agent must reconcile without restarting")
		refreshNUMAConsumers(ctx, h, workers)
		for _, worker := range workers {
			want := expected[worker.Name]
			want.Zones, want.GPUsPerZone = phase.zones, int64(8/phase.zones)
			waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
				return assertions.NUMATopologyProblems(nrt, want)
			})
		}
		want := expected[workers[0].Name]
		want.Zones, want.GPUsPerZone = phase.zones, int64(8/phase.zones)
		checkNUMAFiveGPUAdmission(ctx, h, want, phase.name, phase.zones == 1)
	}
	restored = true
}
