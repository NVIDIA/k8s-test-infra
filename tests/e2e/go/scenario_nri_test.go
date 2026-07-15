//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

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
	nriClusterName    = "nvml-mock-nri"
	nriWorkloadNS     = "default"
	nriAgentDaemonSet = "gpu-agent"
	nriAgentSelector  = "app=gpu-agent"
	nriNRIDaemonSet   = "nvml-mock-nri"

	// nriDomainName / nriDomainUUID identify the single ComputeDomain the
	// generated topology overlay declares. The UUID is arbitrary but must match
	// what check-fabric reports inside the injected pods.
	nriDomainName = "node-wide-domain"
	nriDomainUUID = "00000000-0000-0000-0000-0000000000cd"
)

// Go port of docs/demo/node-wide-injection/run.sh. A dedicated Kind cluster
// with containerd NRI enabled is created once; the nvml-mock chart is installed
// per selected GPU profile with `nri.enabled=true` (plus a per-node
// ComputeDomain overlay for fabric-attached profiles). The scenario then proves
// that an ordinary `gpu-agent` DaemonSet — no `nvidia.com/gpu` request, no
// hostPath/mock volumes, no `MOCK_*` env — sees the full mock GPU stack purely
// through NRI ambient injection, and that each node carries its assigned
// ComputeDomain clique / cluster UUID.
var _ = Describe("nvml-mock node-wide NRI injection", Label("nri"), Ordered, func() {
	var (
		h          *harness.Harness
		workers    []cluster.Node
		topoValues string
	)
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, nriClusterName, assets.KindNRIConfig, "nri")
		var err error
		workers, err = h.Cluster.Workers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(workers)).To(BeNumerically(">=", 2),
			"node-wide NRI scenario needs >= 2 Kind workers for the two-clique overlay, found %d", len(workers))
		topoValues = writeNRITopologyValues(workers)
		DeferCleanup(func() { _ = os.Remove(topoValues) })
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p      profile.Profile
				fabric bool
			)

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				fabric = p.FabricMgr()
				installNRIChart(ctx, h, p, topoValues, fabric)
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
				deployNRIAgent(ctx, h)
			})

			// gpu-agent readiness alone already proves injection (its `set -eu`
			// self-test fails otherwise); this asserts the pod spec stayed plain.
			It("keeps the workload pod plain (no nvidia.com/gpu request)", Label("nri-inject"), func(ctx SpecContext) {
				assertAgentHasNoGPURequest(ctx, h)
			})

			It("reports the profile GPUs via nvidia-smi in the injected pod", Label("nvidia-smi"), func(ctx SpecContext) {
				assertAgentSeesGPUs(ctx, h, p.ExpectedGPUs())
			})

			It("carries per-node ComputeDomain fabric identity through NRI", Label("compute-domain"), func(ctx SpecContext) {
				if !fabric {
					Skip("profile " + name + " has no NVLink fabric; the ComputeDomain overlay is a no-op")
				}
				assertNodeCliqueIdentities(ctx, h, workers)
			})
		})
	}
})

// installNRIChart (re)installs the nvml-mock release with the NRI plugin
// enabled. Fabric-attached profiles additionally get the generated
// ComputeDomain overlay via `-f` (a structured merge of topology.domains, never
// --set-file which would stuff the raw bytes in as a string literal).
func installNRIChart(ctx context.Context, h *harness.Harness, p profile.Profile, topoValues string, fabric bool) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
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
			"nri.enabled":      "true",
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
	if fabric {
		rel.ValuesFiles = []string{topoValues}
	}
	By("helm upgrade --install nvml-mock with NRI enabled (profile=" + p.Name + ")")
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed(), "helm upgrade --install nvml-mock with NRI (profile=%s)", p.Name)
}

// deployNRIAgent (re)creates the plain gpu-agent DaemonSet. It deletes any
// prior instance first so containers are created AFTER the nvml-mock daemon
// staged the overlay — NRI only injects at container-creation time — then waits
// for readiness, which fails unless every pod's ambient self-test passed.
func deployNRIAgent(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Kube.Delete(ctx, assets.NRIGpuAgentManifest)).To(Succeed(), "delete previous gpu-agent DaemonSet")
	Expect(h.Kube.Apply(ctx, assets.NRIGpuAgentManifest)).To(Succeed(), "apply gpu-agent DaemonSet")
	assertions.WaitDaemonSetReady(ctx, h.Kube, nriWorkloadNS, nriAgentDaemonSet, config.ReadyTimeout(), config.PollInterval())
}

// assertAgentHasNoGPURequest mirrors run.sh's "gpu-agent has no nvidia.com/gpu
// resource request" guard: node-wide injection must not depend on the extended
// resource being requested.
func assertAgentHasNoGPURequest(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	out, err := h.Kube.KubectlCombined(ctx, "get", "daemonset", "-n", nriWorkloadNS, nriAgentDaemonSet,
		"-o", "jsonpath={.spec.template.spec.containers[0].resources}")
	Expect(err).NotTo(HaveOccurred(), "read gpu-agent container resources")
	Expect(out).NotTo(ContainSubstring(kube.GPUResourceName),
		"gpu-agent must not request %s; node-wide injection is ambient (resources=%s)", kube.GPUResourceName, out)
}

// assertAgentSeesGPUs execs `nvidia-smi -L` in a gpu-agent pod and asserts the
// NRI-injected overlay exposes exactly the profile's GPU count.
func assertAgentSeesGPUs(ctx context.Context, h *harness.Harness, expectedGPUs int) {
	GinkgoHelper()
	pod := firstNRIAgentPod(ctx, h)
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "-L")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L in gpu-agent pod: %s", res.Combined())
	Expect(countGPULines(res.Combined())).To(Equal(expectedGPUs),
		"gpu-agent should see %d NRI-injected GPUs via nvidia-smi -L\n%s", expectedGPUs, strings.TrimSpace(res.Combined()))
}

// assertNodeCliqueIdentities runs the staged `check-fabric` consumer inside the
// gpu-agent pod on every worker and asserts each node reports the clique /
// cluster UUID the topology overlay assigned to it — with no nvidia.com/gpu
// request and no MOCK_* env in the pod spec (identity comes only from NRI).
func assertNodeCliqueIdentities(ctx context.Context, h *harness.Harness, workers []cluster.Node) {
	GinkgoHelper()
	cliqueByNode := nriCliqueByNode(workers)
	for _, w := range workers {
		expected := cliqueByNode[w.Name]
		pod := nriAgentPodOnNode(ctx, h, w.Name)
		res, err := h.Kube.ExecSh(ctx, pod, "check-fabric 2>&1")
		Expect(err).NotTo(HaveOccurred(), "check-fabric on %s: %s", w.Name, res.Combined())
		out := res.Combined()
		Expect(out).To(ContainSubstring(fmt.Sprintf("cliqueId    : %d", expected)),
			"%s: expected cliqueId %d from check-fabric\n%s", w.Name, expected, strings.TrimSpace(out))
		Expect(strings.ToLower(out)).To(ContainSubstring(strings.ToLower("clusterUuid : "+nriDomainUUID)),
			"%s: expected clusterUuid %s from check-fabric\n%s", w.Name, nriDomainUUID, strings.TrimSpace(out))
	}
}

func firstNRIAgentPod(ctx context.Context, h *harness.Harness) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		n, err := h.Kube.FirstPodName(ctx, nriWorkloadNS, nriAgentSelector)
		name = n
		return n, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no gpu-agent pod found")
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

func nriAgentPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		out, err := h.Kube.KubectlCombined(ctx, "get", "pod", "-n", nriWorkloadNS, "-l", nriAgentSelector,
			"--field-selector", "spec.nodeName="+node, "-o", "jsonpath={.items[0].metadata.name}")
		name = strings.TrimSpace(out)
		return name, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no gpu-agent pod on node %s", node)
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

func writeNRITopologyValues(workers []cluster.Node) string {
	GinkgoHelper()
	path, err := assets.WriteTemp("nri-topology-*.yaml", nriTopologyValues(workers))
	Expect(err).NotTo(HaveOccurred(), "write NRI topology values")
	return path
}

// nriTopologyValues renders a two-clique ComputeDomain overlay values fragment
// from the discovered worker names (first half -> clique 0, second half ->
// clique 1), keeping the overlay cluster-name agnostic.
func nriTopologyValues(workers []cluster.Node) []byte {
	mid := (len(workers) + 1) / 2
	var b strings.Builder
	b.WriteString("# Generated by the node-wide NRI injection e2e scenario.\n")
	b.WriteString("topology:\n")
	b.WriteString("  enabled: true\n")
	b.WriteString("  domains:\n")
	b.WriteString("    - name: " + nriDomainName + "\n")
	b.WriteString("      uuid: \"" + nriDomainUUID + "\"\n")
	b.WriteString("      cliques:\n")
	for cliqueID, group := range [][]cluster.Node{workers[:mid], workers[mid:]} {
		b.WriteString(fmt.Sprintf("        - id: %d\n", cliqueID))
		b.WriteString("          nodes:\n")
		for _, n := range group {
			b.WriteString("            - " + n.Name + "\n")
		}
	}
	return []byte(b.String())
}

func nriCliqueByNode(workers []cluster.Node) map[string]int {
	mid := (len(workers) + 1) / 2
	m := make(map[string]int, len(workers))
	for _, n := range workers[:mid] {
		m[n.Name] = 0
	}
	for _, n := range workers[mid:] {
		m[n.Name] = 1
	}
	return m
}

func countGPULines(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "GPU ") {
			count++
		}
	}
	return count
}
