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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	nriClusterName    = "nvml-mock-nri"
	nriWorkloadNS     = "default"
	nriAgentDaemonSet = "gpu-agent"
	nriAgentSelector  = "app=gpu-agent"
	nriNRIDaemonSet   = "nvml-mock-nri"
	nriPluginSelector = "app.kubernetes.io/name=nvml-mock-nri"

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
				p             profile.Profile
				computeDomain bool
			)

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				// Only profiles that declare a device_defaults.fabric block
				// (h100/gb200/gb300) expose ComputeDomain identity; NVSwitch-only
				// profiles like a100 report "fabric NOT SUPPORTED".
				computeDomain = p.HasFabric()
				installNRIChart(ctx, h, p, topoValues, computeDomain)
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
				if !computeDomain {
					Skip("profile " + name + " declares no device_defaults.fabric block; ComputeDomain identity is unsupported")
				}
				assertNodeCliqueIdentities(ctx, h, workers)
			})
		})
	}

	// Failure-mode hardening (#434). Everything above asserts that injection
	// works. This asserts what happens when it stops working mid-run.
	//
	// The specs above cannot catch that on their own, and not by oversight:
	// injection is baked into the OCI spec at container-creation time, so pods
	// created while the plugin was healthy keep their injection no matter what
	// happens to the plugin afterwards. A suite that creates its pods early and
	// asserts later passes cleanly against a node that lost injection halfway
	// through. Only pods created *after* the failure come up unmocked.
	//
	// SIGSTOP is the honest reproduction of a wedged plugin: the process stays
	// alive, the ttRPC connection stays open, and the handler never answers.
	// `pgrep nvml-mock-nri` and any check that only proves the socket is bound
	// both report healthy throughout.
	Context("when the plugin wedges mid-run", Label("nri-failure"), Ordered, func() {
		var (
			victim     cluster.Node
			pluginPod  kube.PodRef
			restartsAt int
		)

		BeforeAll(func(ctx SpecContext) {
			Expect(selectedProfiles).NotTo(BeEmpty())
			p := loadProfile(selectedProfiles[0])
			installNRIChart(ctx, h, p, topoValues, p.HasFabric())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
			deployNRIAgent(ctx, h)

			victim = workers[0]
			pluginPod = nriPluginPodOnNode(ctx, h, victim.Name)
			var err error
			restartsAt, err = nriRestartCount(ctx, h, pluginPod)
			Expect(err).NotTo(HaveOccurred(), "read baseline restart count")

			By("SIGSTOP the nvml-mock-nri process on " + victim.Name)
			wedgeNRIPlugin(ctx, victim.Name)
		})

		// The readiness probe is the detectable half of the fail-open posture:
		// without it the DaemonSet keeps reporting its full desired count while
		// the node silently injects nothing.
		It("reports the wedged node as NotReady", Label("nri-failure-detect"), func(ctx SpecContext) {
			Eventually(func() (bool, error) {
				return h.Kube.DaemonSetReady(ctx, nvmlMockNamespace, nriNRIDaemonSet)
			}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(time.Second).
				Should(BeFalse(), "daemonset %s/%s stayed Ready while the plugin on %s was wedged",
					nvmlMockNamespace, nriNRIDaemonSet, victim.Name)
		})

		// The liveness probe is the recovery half. A wedged plugin cannot exit
		// on its own -- unlike a dropped connection, which makes stub.Run
		// return and the process exit -- so only a probe-driven restart clears
		// it. Asserting the kubelet's own reason pins that the restart came
		// from the liveness probe and not from something incidental.
		It("restarts the wedged plugin", Label("nri-failure-recover"), func(ctx SpecContext) {
			Eventually(func() (int, error) {
				return nriRestartCount(ctx, h, pluginPod)
			}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
				Should(BeNumerically(">", restartsAt),
					"plugin container on %s never restarted; a wedged plugin cannot recover on its own", victim.Name)

			described, err := h.Kube.DescribePod(ctx, pluginPod.Namespace, pluginPod.Pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(described).To(ContainSubstring("Liveness probe failed"),
				"restart was not attributed to the liveness probe:\n%s", described)
		})

		// The acceptance criterion: a pod created after the failure must be
		// correctly injected once the plugin recovers. This is the assertion
		// the positive specs above structurally cannot make.
		It("injects into a workload created after the wedge", Label("nri-failure-inject"), func(ctx SpecContext) {
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())

			By("recreating gpu-agent so its containers are created after the wedge")
			deployNRIAgent(ctx, h)

			p := loadProfile(selectedProfiles[0])
			pod := nriAgentPodOnNode(ctx, h, victim.Name)
			res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "-L")
			Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L in the post-wedge gpu-agent pod: %s", res.Combined())
			Expect(countGPULines(res.Combined())).To(Equal(p.ExpectedGPUs()),
				"a pod created after the wedge must still see %d injected GPUs\n%s",
				p.ExpectedGPUs(), strings.TrimSpace(res.Combined()))
		})
	})
})

// nriPluginPodOnNode returns the nvml-mock-nri pod scheduled on node.
func nriPluginPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		pods, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, nriPluginSelector)
		if err != nil {
			return "", err
		}
		for _, pod := range pods {
			podNode, err := h.Kube.PodNode(ctx, nvmlMockNamespace, pod)
			if err != nil {
				return "", err
			}
			if podNode == node {
				name = pod
				return name, nil
			}
		}
		return "", nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running nvml-mock-nri pod on node %s", node)
	return kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
}

func nriRestartCount(ctx context.Context, h *harness.Harness, pod kube.PodRef) (int, error) {
	out, err := h.Kube.KubectlCombined(ctx, "get", "pod", "-n", pod.Namespace, pod.Pod,
		"-o", "jsonpath={.status.containerStatuses[0].restartCount}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// wedgeNRIPlugin freezes the plugin process with SIGSTOP, reproducing a handler
// that never answers while the process and its runtime connection stay up.
//
// The signal is sent from the Kind node's PID namespace, not from inside the
// container, and that is load-bearing: the plugin is PID 1 of its container's
// PID namespace, and the kernel discards signals with a default action that are
// sent to a namespace init from within that same namespace. `kubectl exec ...
// kill -STOP 1` is silently a no-op. From the node -- an ancestor namespace --
// the signal is delivered.
func wedgeNRIPlugin(ctx context.Context, node string) {
	GinkgoHelper()
	pid := nriPluginHostPID(ctx, node)
	_, err := runner.Run(ctx, "docker", "exec", node, "kill", "-STOP", pid)
	Expect(err).NotTo(HaveOccurred(), "SIGSTOP nvml-mock-nri (pid %s) on %s", pid, node)

	// Confirm the process really is stopped rather than trusting kill's exit
	// code: a wedge that did not take would make every assertion below vacuous.
	Eventually(func() (string, error) {
		res, err := runner.RunQuiet(ctx, "docker", "exec", node, "cat", "/proc/"+pid+"/stat")
		if err != nil {
			return "", err
		}
		fields := strings.Fields(res.Stdout)
		if len(fields) < 3 {
			return "", fmt.Errorf("unexpected /proc/%s/stat: %q", pid, res.Stdout)
		}
		return fields[2], nil // state: T = stopped
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(time.Second).
		Should(Equal("T"), "nvml-mock-nri (pid %s) on %s did not enter the stopped state", pid, node)
}

// nriPluginHostPID finds the plugin process in the Kind node's PID namespace.
// It reads /proc/<pid>/exe rather than shelling out to pgrep: procps is not
// guaranteed in the node image, and matching on a command line would also match
// the matching process itself.
func nriPluginHostPID(ctx context.Context, node string) string {
	GinkgoHelper()
	const script = `for p in /proc/[0-9]*; do case "$(readlink "$p/exe" 2>/dev/null)" in */nvml-mock-nri) echo "${p##*/}";; esac; done`
	res, err := runner.Run(ctx, "docker", "exec", node, "sh", "-c", script)
	Expect(err).NotTo(HaveOccurred(), "locate nvml-mock-nri on %s: %s", node, res.Combined())

	pids := strings.Fields(res.Stdout)
	Expect(pids).NotTo(BeEmpty(), "no nvml-mock-nri process found on %s", node)
	return pids[0]
}

// installNRIChart (re)installs the nvml-mock release with the NRI plugin
// enabled. Fabric-attached profiles additionally get the generated
// ComputeDomain overlay via `-f` (a structured merge of topology.domains, never
// --set-file which would stuff the raw bytes in as a string literal).
func installNRIChart(ctx context.Context, h *harness.Harness, p profile.Profile, topoValues string, withComputeDomain bool) {
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
	if withComputeDomain {
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
		pods, err := h.Kube.RunningPodNames(ctx, nriWorkloadNS, nriAgentSelector)
		if err != nil || len(pods) == 0 {
			return "", err
		}
		name = pods[0]
		return name, nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running gpu-agent pod found")
	return kube.PodRef{Namespace: nriWorkloadNS, Pod: name}
}

func nriAgentPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		pods, err := h.Kube.RunningPodNames(ctx, nriWorkloadNS, nriAgentSelector)
		if err != nil {
			return "", err
		}
		for _, pod := range pods {
			podNode, err := h.Kube.PodNode(ctx, nriWorkloadNS, pod)
			if err != nil {
				return "", err
			}
			if podNode == node {
				name = pod
				return name, nil
			}
		}
		return "", nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running gpu-agent pod on node %s", node)
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
