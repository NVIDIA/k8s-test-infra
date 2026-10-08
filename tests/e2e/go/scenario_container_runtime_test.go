//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

const (
	runtimeWorkloadNS = "default"
	// mokkaConfigFile is Mokka's config file in containerd's config dir.
	mokkaConfigFile = "/etc/containerd/conf.d/50-mokka.toml"
)

// runtimeClass is the RuntimeClass the GPU Operator creates for the handler
// the node daemon registers, which the operator's own pods name. The scenario
// creates it only where no operator has.
var runtimeClass = []byte(`apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: nvidia
handler: nvidia
`)

// The container runtime setup (MEP-0006): the node daemon installs the NVIDIA
// runtime and registers it with containerd as the default nvidia handler on
// stock kindest/node, and reverts it when its pod stops. Every other scenario depends
// on the setup implicitly, since the node pod is not ready until it succeeds;
// this one checks what it leaves on the node. The last specs restart
// containerd, so they run after the ones that only look.
var _ = Describe("nvml-mock container runtime", Label("container-runtime"), Ordered, func() {
	var (
		h *harness.Harness
		// nodes is the simulated-GPU fleet: the node daemon, and so the
		// setup, runs nowhere else.
		nodes []cluster.Node
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "container-runtime")
		DeferCleanup(func(ctx SpecContext) { collectContainerRuntimeOnFailure(ctx, h, nodes) })

		assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
		nodes = sgpuNodes(ctx, h)
	})

	It("registers the nvidia handler as the default on every node", func(ctx SpecContext) {
		for _, n := range nodes {
			rt := criInfo(ctx, n)
			Expect(assertions.CheckNvidiaHandler(rt)).To(Succeed(), "node %s", n.Name)
			Expect(rt.CDISpecDirs).To(ContainElement("/var/run/cdi"), "node %s", n.Name)

			for _, binary := range []string{"nvidia-container-runtime", "nvidia-ctk", "nvidia-cdi-hook"} {
				nodeOutput(ctx, n, "test", "-x", "/usr/bin/"+binary)
			}
			Expect(nodeOutput(ctx, n, "cat", "/etc/nvidia-container-runtime/config.toml")).
				To(ContainSubstring(`mode = "cdi"`), "node %s", n.Name)
		}
	})

	It("registers the handler in Mokka's config file, which config.toml imports", func(ctx SpecContext) {
		for _, n := range nodes {
			config := nodeOutput(ctx, n, "cat", mokkaConfigFile)
			Expect(config).To(ContainSubstring(assertions.NvidiaRuntimeBinary), "node %s", n.Name)
			Expect(config).To(ContainSubstring("enable_cdi = true"), "node %s", n.Name)
			Expect(nodeOutput(ctx, n, "cat", "/etc/containerd/config.toml")).
				To(ContainSubstring("/etc/containerd/conf.d/*.toml"), "node %s", n.Name)
		}
	})

	It("gives a pod that sets NVIDIA_VISIBLE_DEVICES the node's mock GPUs", func(ctx SpecContext) {
		n := nodes[0]

		ref := runWorkload(ctx, h, "default-runtime-class", n, "")
		res, err := h.Kube.ExecSh(ctx, ref, "nvidia-smi -L")
		Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L through the default handler: %s", res.Combined())
		Expect(gpuLines(res.Stdout)).To(Equal(nodeGPUs(ctx, h, n)), "the pod sees the node's mock GPUs")

		res, err = h.Kube.ExecSh(ctx, ref, "ldconfig -p | grep -c libnvidia-ml.so.1")
		Expect(err).NotTo(HaveOccurred(), "nvidia-cdi-hook refreshes the linker cache: %s", res.Combined())
	})

	It("gives a runtimeClassName: nvidia pod the same GPUs, as the GPU Operator's pods get them", func(ctx SpecContext) {
		ensureRuntimeClass(ctx, h)
		n := nodes[0]

		ref := runWorkload(ctx, h, "nvidia-runtime-class", n, "nvidia")
		res, err := h.Kube.ExecSh(ctx, ref, "nvidia-smi -L")
		Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L through RuntimeClass nvidia: %s", res.Combined())
		Expect(gpuLines(res.Stdout)).To(Equal(nodeGPUs(ctx, h, n)))
	})

	It("sets the runtime up again when the node pod is replaced", func(ctx SpecContext) {
		n := nodes[0]
		before := containerdPID(ctx, n)

		Expect(h.Kube.DeletePodsByLabel(ctx, nvmlMockNamespace, nvmlMockSelector)).To(Succeed())
		assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
		assertions.WaitNodeReady(ctx, h.Kube, n.Name, config.ReadyTimeout(), config.PollInterval())

		Expect(containerdPID(ctx, n)).NotTo(Equal(before), "the old pod reverts the setup and the new one applies it")
		Expect(assertions.CheckNvidiaHandler(criInfo(ctx, n))).To(Succeed(), "node %s", n.Name)
	})

	It("restores a deleted config file when the node pod restarts", func(ctx SpecContext) {
		n := nodes[0]
		nodeOutput(ctx, n, "rm", "-f", mokkaConfigFile)

		restartNodePod(ctx, h, n)

		nodeOutput(ctx, n, "test", "-f", mokkaConfigFile)
		Expect(assertions.CheckNvidiaHandler(criInfo(ctx, n))).To(Succeed(), "node %s", n.Name)
	})

	It("removes the handler from a node that loses its node pod", func(ctx SpecContext) {
		n := nodes[len(nodes)-1]
		DeferCleanup(func(ctx SpecContext) {
			_, err := h.Kube.KubectlCombined(ctx, "label", "node", n.Name, "--overwrite", sgpuNodeLabel+"="+sgpuNodeLabelValue)
			Expect(err).NotTo(HaveOccurred())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			Expect(assertions.CheckNvidiaHandler(criInfo(ctx, n))).To(Succeed(), "the node is set up again")
		})

		_, err := h.Kube.KubectlCombined(ctx, "label", "node", n.Name, sgpuNodeLabel+"-")
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() (map[string]string, error) {
			out, err := runner.RunQuiet(ctx, "docker", "exec", n.Container, "crictl", "info")
			if err != nil {
				return nil, err
			}
			rt, err := assertions.ParseCRIInfo([]byte(out.Stdout))

			return rt.Handlers, err
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
			ShouldNot(HaveKey(assertions.NvidiaHandler), "the stopping node pod reverts the setup on %s", n.Name)
		nodeOutput(ctx, n, "test", "!", "-e", mokkaConfigFile)
	})
})

// sgpuNodes returns the nodes labelled as the simulated-GPU fleet, sorted by
// name.
func sgpuNodes(ctx context.Context, h *harness.Harness) []cluster.Node {
	GinkgoHelper()
	all, err := h.Cluster.Nodes(ctx)
	Expect(err).NotTo(HaveOccurred(), "list cluster nodes")

	var nodes []cluster.Node
	for _, n := range all {
		v, ok, err := h.Kube.NodeLabel(ctx, n.Name, sgpuNodeLabel)
		Expect(err).NotTo(HaveOccurred(), "read label %s on node %s", sgpuNodeLabel, n.Name)
		if ok && v == sgpuNodeLabelValue {
			nodes = append(nodes, n)
		}
	}
	Expect(nodes).NotTo(BeEmpty(), "no node carries %s=%s", sgpuNodeLabel, sgpuNodeLabelValue)

	return nodes
}

// nodeOutput runs a command inside a Kind node's container and returns its
// stdout, failing the spec if it fails.
func nodeOutput(ctx context.Context, n cluster.Node, args ...string) string {
	GinkgoHelper()
	res, err := runner.RunQuiet(ctx, "docker", append([]string{"exec", n.Container}, args...)...)
	Expect(err).NotTo(HaveOccurred(), "%s on %s: %s", strings.Join(args, " "), n.Name, res.Combined())

	return res.Stdout
}

// criInfo reads what the node's running containerd reports over CRI.
func criInfo(ctx context.Context, n cluster.Node) assertions.ContainerRuntime {
	GinkgoHelper()
	rt, err := assertions.ParseCRIInfo([]byte(nodeOutput(ctx, n, "crictl", "info")))
	Expect(err).NotTo(HaveOccurred(), "crictl info on %s", n.Name)

	return rt
}

func containerdPID(ctx context.Context, n cluster.Node) string {
	GinkgoHelper()

	return strings.TrimSpace(nodeOutput(ctx, n, "systemctl", "show", "containerd", "--property=MainPID", "--value"))
}

// ensureRuntimeClass creates the nvidia RuntimeClass unless the GPU Operator,
// which owns it where it runs, already has. A RuntimeClass the scenario
// created is deleted after it.
func ensureRuntimeClass(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	if _, err := h.Kube.KubectlCombined(ctx, "get", "runtimeclass", assertions.NvidiaHandler); err == nil {
		return
	}

	Expect(h.Kube.Apply(ctx, runtimeClass)).To(Succeed(), "create RuntimeClass %s", assertions.NvidiaHandler)
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, runtimeClass) }) //nolint:contextcheck // Ginkgo cleanup ctx is intentionally distinct from the outer spec ctx
}

// runWorkload starts a pod on n that sets NVIDIA_VISIBLE_DEVICES=all, under
// runtimeClassName when it is set, and returns a reference to exec into it.
func runWorkload(ctx context.Context, h *harness.Harness, name string, n cluster.Node, runtimeClassName string) kube.PodRef {
	GinkgoHelper()
	manifest := pod.Spec{
		Name:             name,
		Namespace:        runtimeWorkloadNS,
		Image:            nriWorkloadImage,
		Node:             n.Name,
		RuntimeClassName: runtimeClassName,
		Env:              map[string]string{"NVIDIA_VISIBLE_DEVICES": "all"},
		// Kept alive for exec, and quick to stop: see nriWorkloadSpec.
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"trap 'exit 0' TERM; sleep 3600 & wait"},
	}.Render()

	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "create pod %s", name)
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) }) //nolint:contextcheck // Ginkgo cleanup ctx is intentionally distinct from the outer spec ctx
	assertions.WaitPodPhase(ctx, h.Kube, runtimeWorkloadNS, name, "Running", config.ReadyTimeout(), config.PollInterval())

	return kube.PodRef{Namespace: runtimeWorkloadNS, Pod: name, Container: pod.DefaultContainerName}
}

// nodeGPUs counts the GPUs the node daemon's own nvidia-smi lists on n.
func nodeGPUs(ctx context.Context, h *harness.Harness, n cluster.Node) int {
	GinkgoHelper()
	ref := nvmlPodOnNode(ctx, h, n.Name)
	ref.Container = "node-agent"

	res, err := h.Kube.ExecSh(ctx, ref, "nvidia-smi -L")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L in the node pod on %s: %s", n.Name, res.Combined())

	count := gpuLines(res.Stdout)
	Expect(count).To(BeNumerically(">", 0), "the node pod on %s lists no GPUs", n.Name)

	return count
}

func gpuLines(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "GPU ") {
			count++
		}
	}

	return count
}

// restartNodePod replaces the node pod on n and waits for the node and the
// DaemonSet to settle, since each pod start restarts containerd.
func restartNodePod(ctx context.Context, h *harness.Harness, n cluster.Node) {
	GinkgoHelper()
	ref := nvmlPodOnNode(ctx, h, n.Name)

	_, err := h.Kube.KubectlCombined(ctx, "delete", "pod", "-n", nvmlMockNamespace, ref.Pod)
	Expect(err).NotTo(HaveOccurred(), "delete node pod %s", ref.Pod)
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	assertions.WaitNodeReady(ctx, h.Kube, n.Name, config.ReadyTimeout(), config.PollInterval())
}

func collectContainerRuntimeOnFailure(ctx context.Context, h *harness.Harness, nodes []cluster.Node) {
	if !CurrentSpecReport().Failed() || h == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, "container-runtime")
	for _, n := range nodes {
		c.NodeExec(ctx, n.Name+"-containerd-journal.txt", n.Container, "journalctl", "-u", "containerd", "--no-pager", "-n", "200")
		c.NodeExec(ctx, n.Name+"-crictl-info.json", n.Container, "crictl", "info")
		c.NodeExec(ctx, n.Name+"-containerd-config.txt", n.Container, "sh", "-c",
			"ls -l /etc/containerd/conf.d; cat /etc/containerd/config.toml /etc/containerd/conf.d/*.toml")
		c.NodeExec(ctx, n.Name+"-toolkit.txt", n.Container, "ls", "-l", "/usr/bin/nvidia-container-runtime",
			"/usr/bin/nvidia-ctk", "/usr/bin/nvidia-cdi-hook", "/usr/local/nvml-mock/toolkit")
	}
	c.Kubectl(ctx, "runtimeclasses.yaml", "get", "runtimeclasses", "-o", "yaml")
}
