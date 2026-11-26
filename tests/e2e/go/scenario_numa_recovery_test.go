//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

const numaAbsentFile = "<numa-e2e-file-absent>"

var numaCheckpointPaths = []string{
	"/var/lib/kubelet/cpu_manager_state",
	"/var/lib/kubelet/memory_manager_state",
}

type numaNodeSetup struct {
	Files  map[string]string
	Mounts map[string]string
}

func numaFixtureValues(enabled bool) string {
	GinkgoHelper()
	var values map[string]any
	Expect(yaml.Unmarshal(assets.NUMAValues, &values)).To(Succeed())
	values["nodeAgent"].(map[string]any)["numa"].(map[string]any)["enabled"] = enabled
	data, err := yaml.Marshal(values)
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

func numaOptionalHostFile(ctx context.Context, node cluster.Node, path string) (string, error) {
	result, err := runner.RunQuiet(ctx, "docker", "exec", node.Container, "sh", "-c",
		`if [ -e "$1" ]; then cat "$1"; else printf '%s' "$2"; fi`, "sh", path, numaAbsentFile)
	return result.Stdout, err
}

func readNUMANodeSetup(ctx context.Context, node cluster.Node) (numaNodeSetup, error) {
	state := numaNodeSetup{Files: map[string]string{}, Mounts: map[string]string{}}
	paths := append([]string{
		"/var/lib/kubelet/config.yaml",
		"/sys/devices/system/node/online",
		"/sys/devices/system/node/possible",
		"/sys/devices/system/node/has_cpu",
		"/sys/devices/system/node/has_memory",
	}, numaCheckpointPaths...)
	for _, path := range paths {
		data, err := numaOptionalHostFile(ctx, node, path)
		if err != nil {
			return state, fmt.Errorf("read original node file %s: %w", path, err)
		}
		state.Files[path] = data
	}
	for _, path := range []string{"/sys/devices/system/node", "/sys/bus/node/devices", "/var/lib/kubelet/config.yaml"} {
		result, err := runner.RunQuiet(ctx, "docker", "exec", node.Container,
			"findmnt", "--noheadings", "--output", "TARGET,SOURCE,FSTYPE,OPTIONS", "--target", path)
		if err != nil {
			return state, fmt.Errorf("read original mount at %s: %w", path, err)
		}
		state.Mounts[path] = strings.TrimSpace(result.Stdout)
	}
	return state, nil
}

func waitNUMARestored(ctx SpecContext, h *harness.Harness, node cluster.Node) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		for _, path := range []string{
			"/run/mokka/numa/.applied", "/run/mokka/numa/.pending", "/run/mokka/numa/original",
			"/var/lib/nvml-mock/kubelet/boot-reset.enabled",
			"/etc/systemd/system/kubelet.service.d/30-mokka-reset.conf",
			"/etc/systemd/system/mokka-kubelet-reset.service",
		} {
			data, err := numaOptionalHostFile(ctx, node, path)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(data).To(Equal(numaAbsentFile), path)
		}
		active, err := runner.RunQuiet(ctx, "docker", "exec", node.Container, "systemctl", "is-active", "kubelet")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(active.Stdout)).To(Equal("active"))
		ready, err := h.Kube.NodeReady(ctx, node.Name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ready).To(BeTrue())
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
}

func readNUMAWorkload(ctx context.Context, h *harness.Harness, name string) corev1.Pod {
	GinkgoHelper()
	var workload corev1.Pod
	Expect(h.Kube.GetJSON(ctx, &workload, "pod", name, "-n", "default")).To(Succeed())
	Expect(workload.Status.Phase).To(Equal(corev1.PodRunning))
	Expect(workload.Status.Conditions).To(ContainElement(And(
		HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
	Expect(workload.Status.ContainerStatuses).To(HaveLen(1))
	Expect(workload.Status.ContainerStatuses[0].ContainerID).NotTo(BeEmpty())
	return workload
}

func applyNUMAOneGPUWorkload(ctx SpecContext, h *harness.Harness, want assertions.NUMAExpectation, name string) ([]byte, assertions.NodeResourceTopology, assertions.NodeResourceTopology) {
	GinkgoHelper()
	before := waitNUMATopology(ctx, h, want.Node, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMATopologyProblems(nrt, want)
	})
	manifest := numaWorkload(name, want.Node, 1)
	DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, manifest)).To(Succeed()) })
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed())
	assertions.WaitPodPhase(ctx, h.Kube, "default", name, "Running", config.ReadyTimeout(), config.PollInterval())
	request := corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse(numaWorkloadCPU), corev1.ResourceMemory: resource.MustParse(numaWorkloadMemory),
		"nvidia.com/gpu": resource.MustParse("1"),
	}
	allocated := waitNUMATopology(ctx, h, want.Node, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMAAllocationProblems(before, nrt, request)
	})
	return manifest, before, allocated
}

func checkNUMAPodReplacement(ctx SpecContext, h *harness.Harness, worker cluster.Node, want assertions.NUMAExpectation) {
	GinkgoHelper()
	name := "numa-pod-replacement"
	manifest, baseline, allocated := applyNUMAOneGPUWorkload(ctx, h, want, name)
	workload := readNUMAWorkload(ctx, h, name)
	agent := numaAgentIdentities(ctx, h)[worker.Name]
	pid, err := runner.RunQuiet(ctx, "docker", "exec", worker.Container, "systemctl", "show", "kubelet", "--property=MainPID", "--value")
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.TrimSpace(pid.Stdout)).NotTo(BeElementOf("", "0"))
	files := map[string]string{}
	for _, path := range append([]string{"/run/mokka/numa/.applied"}, numaCheckpointPaths...) {
		files[path], err = numaHostFile(ctx, worker, path)
		Expect(err).NotTo(HaveOccurred())
	}
	pod, err := h.Kube.RunningPodOnNode(ctx, nvmlMockNamespace, nvmlMockSelector, worker.Name)
	Expect(err).NotTo(HaveOccurred())
	_, err = runner.Run(ctx, "kubectl", "--context", h.Kube.Context, "delete", "pod", pod, "-n", nvmlMockNamespace, "--wait=true", "--timeout=90s")
	Expect(err).NotTo(HaveOccurred())
	Eventually(func(g Gomega) {
		var pods corev1.PodList
		g.Expect(h.Kube.GetJSON(ctx, &pods, "pods", "-n", nvmlMockNamespace, "-l", nvmlMockSelector)).To(Succeed())
		g.Expect(pods.Items).To(ContainElement(And(
			HaveField("Spec.NodeName", worker.Name),
			HaveField("DeletionTimestamp", BeNil()),
			HaveField("UID", Not(Equal(agent.UID))),
			HaveField("Status.Conditions", ContainElement(And(HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue)))),
		)), "the replacement Mokka pod must become Ready")
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	currentPID, err := runner.RunQuiet(ctx, "docker", "exec", worker.Container, "systemctl", "show", "kubelet", "--property=MainPID", "--value")
	Expect(err).NotTo(HaveOccurred())
	Expect(currentPID.Stdout).To(Equal(pid.Stdout), "replacing Mokka must not restart kubelet")
	for path, before := range files {
		after, err := numaHostFile(ctx, worker, path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before), path)
	}
	after := readNUMAWorkload(ctx, h, name)
	Expect(after.UID).To(Equal(workload.UID))
	Expect(after.Status.ContainerStatuses[0].ContainerID).To(Equal(workload.Status.ContainerStatuses[0].ContainerID))
	Expect(after.Status.ContainerStatuses[0].RestartCount).To(Equal(workload.Status.ContainerStatuses[0].RestartCount))
	// Span several real updater cycles so an old NRT cannot hide a lost allocation.
	Consistently(func() ([]string, error) {
		var nrt assertions.NodeResourceTopology
		if err := h.Kube.GetJSON(ctx, &nrt, numaNRTResource, worker.Name); err != nil {
			return nil, err
		}
		return assertions.NUMAAvailabilityProblems(allocated, nrt), nil
	}).WithContext(ctx).WithTimeout(12 * time.Second).WithPolling(config.PollInterval()).Should(BeEmpty())
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed())
	waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMAAvailabilityProblems(baseline, nrt)
	})
}

func checkNUMADisable(ctx SpecContext, h *harness.Harness, workers []cluster.Node, expected map[string]assertions.NUMAExpectation, original map[string]numaNodeSetup) {
	GinkgoHelper()
	reenabled := false
	DeferCleanup(func(ctx SpecContext) {
		if !reenabled {
			enableNUMAAndRefresh(ctx, h, workers, expected)
		}
	})
	installNvmlMock(ctx, h, loadProfile("a100"), numaFixtureValues(false))
	for _, worker := range workers {
		waitNUMARestored(ctx, h, worker)
		Eventually(func() (numaNodeSetup, error) {
			return readNUMANodeSetup(ctx, worker)
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Equal(original[worker.Name]), "restore original configuration, topology, mounts, and checkpoints on %s", worker.Name)
	}
	enableNUMAAndRefresh(ctx, h, workers, expected)
	reenabled = true
}

func enableNUMAAndRefresh(ctx SpecContext, h *harness.Harness, workers []cluster.Node, expected map[string]assertions.NUMAExpectation) {
	GinkgoHelper()
	installNvmlMock(ctx, h, loadProfile("a100"), string(assets.NUMAValues))
	waitNUMALayout(ctx, h, workers, "0-1", nil)
	refreshNUMAConsumers(ctx, h, workers)
	for _, worker := range workers {
		waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
			return assertions.NUMATopologyProblems(nrt, expected[worker.Name])
		})
	}
}

func checkNUMANodeRestart(ctx SpecContext, h *harness.Harness, workers []cluster.Node, expected map[string]assertions.NUMAExpectation) {
	GinkgoHelper()
	worker := workers[0]
	const sentinel = "/run/mokka/numa-e2e-before-restart"
	_, err := runner.Run(ctx, "docker", "exec", worker.Container, "touch", sentinel)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		_, err := runner.Run(ctx, "docker", "exec", worker.Container, "rm", "-f", sentinel)
		Expect(err).NotTo(HaveOccurred())
	})
	before, err := runner.RunQuiet(ctx, "docker", "inspect", "--format={{.State.StartedAt}}", worker.Container)
	Expect(err).NotTo(HaveOccurred())
	invocation, err := runner.RunQuiet(ctx, "docker", "exec", worker.Container, "systemctl", "show", "mokka-kubelet-reset.service", "--property=InvocationID", "--value")
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.TrimSpace(invocation.Stdout)).NotTo(BeEmpty())
	restartCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = runner.Run(restartCtx, "docker", "restart", "--timeout=30", worker.Container)
	Expect(err).NotTo(HaveOccurred())
	after, err := runner.RunQuiet(ctx, "docker", "inspect", "--format={{.State.StartedAt}}", worker.Container)
	Expect(err).NotTo(HaveOccurred())
	Expect(after.Stdout).NotTo(Equal(before.Stdout), "restart the actual Kind worker, not only its pod")
	waitNUMALayout(ctx, h, workers, "0-1", nil)
	data, err := numaOptionalHostFile(ctx, worker, sentinel)
	Expect(err).NotTo(HaveOccurred())
	Expect(data).To(Equal(numaAbsentFile), "node restart must clear /run")
	done, err := numaHostFile(ctx, worker, "/run/mokka/kubelet-reset.done")
	Expect(err).NotTo(HaveOccurred())
	Expect(done).To(Equal("done"))
	current, err := runner.RunQuiet(ctx, "docker", "exec", worker.Container, "systemctl", "show", "mokka-kubelet-reset.service", "--property=InvocationID", "--value")
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.TrimSpace(current.Stdout)).NotTo(BeEmpty())
	Expect(current.Stdout).NotTo(Equal(invocation.Stdout), "boot cleanup must run again")
	status, err := runner.RunQuiet(ctx, "docker", "exec", worker.Container, "systemctl", "show", "mokka-kubelet-reset.service", "--property=Result", "--value")
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.TrimSpace(status.Stdout)).To(Equal("success"))
	refreshNUMAConsumers(ctx, h, workers)
	manifest, baseline, _ := applyNUMAOneGPUWorkload(ctx, h, expected[worker.Name], "numa-after-restart")
	readNUMAWorkload(ctx, h, "numa-after-restart")
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed())
	waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMAAvailabilityProblems(baseline, nrt)
	})
	checkNUMAFiveGPUAdmission(ctx, h, expected[worker.Name], "numa-after-restart-cross-zone", false)
}
