//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// ComputeDomains through the unmodified NVIDIA DRA driver (#821). On gb200 and
// gb300 the node agent stages what a real node of a multi-node NVLink system
// has: the IMEX kernel
// surface and, from the pinned archive, nvidia-imex and nvidia-imex-ctl. The
// DRA driver runs its own images: its controller starts an IMEX daemon on each
// node a workload of a ComputeDomain lands on, and the daemons of one NVLink
// clique join one IMEX domain.
//
// The cluster is tests/e2e/kind-compute-domain-racks.yaml, two racks of two
// nodes, with the DRA driver installed with ComputeDomains on:
//
//	tilt ci -- --compute-domain --dra
//
// E2E_IMEX_PAYLOAD_IMAGE names an IMEX payload image loaded into the cluster;
// with it the node agent never downloads IMEX.
const (
	cdNamespace = "compute-domain-e2e"
	cdLabelKey  = "resource.nvidia.com/computeDomain"
	// rackLabel is the scenario's input, a Kind node label nothing else
	// writes.
	rackLabel                = "mokka.nvidia.com/clique"
	draKubeletPluginSelector = "dra-driver-nvidia-gpu-component=kubelet-plugin"
	cdChannelDir             = "/dev/nvidia-caps-imex-channels"
	// cdMaxChannels is the DRA driver's channel count per IMEX domain, all of
	// which an allocationMode All claim gets.
	cdMaxChannels = 2048
	// cdHeldFor is how long a held-back workload must stay held back.
	cdHeldFor = 20 * time.Second
	// stagedIMEX is the node agent's IMEX presence signal, as seen from its own
	// container.
	stagedIMEX = "/var/lib/nvml-mock/driver/usr/bin/nvidia-imex.real"
	// gpuClaimPod holds a GPU claim prepared while IMEX was staged.
	gpuClaimPod = "gpu-claim-before-withdrawal"
	// stagedTopology is where the node agent stages the topology for processes
	// that load the mock NVML from the driver root, the kubelet plugin's too.
	stagedTopology = "/var/lib/nvml-mock/driver/config/topology.yaml"
	// nodeIMEXCache is the node agent's download cache, a source of IMEX.
	nodeIMEXCache = "/var/lib/nvml-mock/cache/imex"
)

// rack is one NVLink clique: the workers sharing a mokka.nvidia.com/clique label.
type rack struct {
	clique string // <cluster UUID>.<clique ID>
	nodes  []string
}

var _ = Describe("ComputeDomains through the DRA driver", Label("dra-compute-domain"), Ordered, func() {
	var (
		h     *harness.Harness
		p     profile.Profile
		racks []rack
		topo  string
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "compute-domain")
		profiles := config.SelectedProfileNames()
		Expect(profiles).NotTo(BeEmpty())
		p = loadProfile(profiles[0])
		if !p.MultiNodeNVLink() {
			Skip("profile " + p.Name + " is not multi-node NVLink, so its nodes run no ComputeDomains")
		}

		racks = discoverRacks(ctx, h)
		Expect(len(racks)).To(BeNumerically(">=", 2), "need two racks (%s labels), found %v", rackLabel, racks)
		for _, r := range racks[:2] {
			Expect(len(r.nodes)).To(BeNumerically(">=", 2), "need two nodes in rack %s, found %v", r.clique, r.nodes)
		}
		values, err := rackTopologyValues(racks)
		Expect(err).NotTo(HaveOccurred(), "render the rack topology")
		topo, err = assets.WriteTemp("compute-domain-topology-*.yaml", values)
		Expect(err).NotTo(HaveOccurred(), "write the rack topology")
		DeferCleanup(func() { _ = os.Remove(topo) })

		prepareNodes(ctx, h, p, topo, racks, true)

		Expect(h.Kube.Apply(ctx, cdNamespaceManifest())).To(Succeed(), "create %s", cdNamespace)
		DeferCleanup(func(ctx SpecContext) {
			Expect(h.Kube.Delete(ctx, cdNamespaceManifest())).To(Succeed(), "delete %s", cdNamespace)
		})
	})

	AfterEach(func(ctx SpecContext) {
		collectComputeDomainOnFailure(ctx, h)
	})

	It("joins the two nodes of a rack into one IMEX domain", func(ctx SpecContext) {
		r := racks[0]
		applyComputeDomain(ctx, h, "rack-1", 2, "Single")
		applyCDWorkload(ctx, h, "rack-1-a", "rack-1", r.nodes[0])
		applyCDWorkload(ctx, h, "rack-1-b", "rack-1", r.nodes[1])
		waitCDWorkloadRunning(ctx, h, "rack-1-a")
		waitCDWorkloadRunning(ctx, h, "rack-1-b")

		Eventually(func(g Gomega) {
			cd := getComputeDomain(ctx, g, h, "rack-1")
			g.Expect(cd.Status.Status).To(Equal("Ready"))
			g.Expect(cd.nodeNames()).To(ConsistOf(r.nodes[0], r.nodes[1]))
			g.Expect(cd.cliques()).To(ConsistOf(r.clique))
			g.Expect(daemonNodes(ctx, g, h, cd.Metadata.UID, true)).To(ConsistOf(r.nodes[0], r.nodes[1]))
		}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
			Should(Succeed(), "both rack-1 nodes join one Ready IMEX domain in clique %s", r.clique)
	})

	It("gives a Single-mode workload channel 0 only", func(ctx SpecContext) {
		Expect(cdChannels(ctx, h, "rack-1-a")).To(Equal([]string{"channel0"}))
	})

	It("drops a node from the domain when its workload leaves", func(ctx SpecContext) {
		r := racks[0]
		deleteCDWorkload(ctx, h, "rack-1-b")

		Eventually(func(g Gomega) {
			cd := getComputeDomain(ctx, g, h, "rack-1")
			g.Expect(cd.nodeNames()).To(ConsistOf(r.nodes[0]))
			g.Expect(daemonNodes(ctx, g, h, cd.Metadata.UID, false)).To(ConsistOf(r.nodes[0]), "the daemon on %s is gone", r.nodes[1])
		}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
			Should(Succeed(), "%s leaves rack-1's IMEX domain", r.nodes[1])
	})

	It("keeps another rack in an IMEX domain of its own", func(ctx SpecContext) {
		r := racks[1]
		applyComputeDomain(ctx, h, "rack-2", 2, "Single")
		applyCDWorkload(ctx, h, "rack-2-a", "rack-2", r.nodes[0])
		applyCDWorkload(ctx, h, "rack-2-b", "rack-2", r.nodes[1])
		waitCDWorkloadRunning(ctx, h, "rack-2-a")
		waitCDWorkloadRunning(ctx, h, "rack-2-b")

		Eventually(func(g Gomega) {
			cd := getComputeDomain(ctx, g, h, "rack-2")
			g.Expect(cd.Status.Status).To(Equal("Ready"))
			g.Expect(cd.nodeNames()).To(ConsistOf(r.nodes[0], r.nodes[1]))
			g.Expect(cd.cliques()).To(ConsistOf(r.clique))
		}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
			Should(Succeed(), "rack-2 forms its own Ready IMEX domain in clique %s", r.clique)
		Expect(r.clique).NotTo(Equal(racks[0].clique))
		rack1 := getComputeDomain(ctx, Default, h, "rack-1")
		Expect(rack1.nodeNames()).To(ConsistOf(racks[0].nodes[0]), "rack-1's domain gains no rack-2 node")
	})

	It("gives an All-mode workload every channel of its domain", func(ctx SpecContext) {
		// rack-1's second node is free again since its workload left.
		node := racks[0].nodes[1]
		applyComputeDomain(ctx, h, "all-channels", 1, "All")
		applyCDWorkload(ctx, h, "all-channels-a", "all-channels", node)
		waitCDWorkloadRunning(ctx, h, "all-channels-a")

		channels := cdChannels(ctx, h, "all-channels-a")
		Expect(channels).To(HaveLen(cdMaxChannels))
		Expect(channels).To(ContainElements("channel0", "channel"+strconv.Itoa(cdMaxChannels-1)))

		deleteCDWorkload(ctx, h, "all-channels-a")
		Expect(h.Kube.Delete(ctx, computeDomainManifest("all-channels", 1, "All"))).To(Succeed())
	})

	// A node holds one IMEX channel 0, so a second workload pinned to a busy
	// node is not scheduled until the first releases it.
	It("holds a second workload on a busy node until the first leaves", func(ctx SpecContext) {
		node := racks[0].nodes[0]
		applyCDWorkload(ctx, h, "rack-1-c", "rack-1", node)
		Consistently(func(g Gomega) {
			g.Expect(cdWorkloadState(ctx, g, h, "rack-1-c").scheduled()).To(BeFalse())
		}).WithContext(ctx).WithTimeout(cdHeldFor).WithPolling(config.PollInterval()).
			Should(Succeed(), "rack-1-c must wait while rack-1-a holds %s", node)

		deleteCDWorkload(ctx, h, "rack-1-a")
		waitCDWorkloadRunning(ctx, h, "rack-1-c")
	})

	// A node takes part in one ComputeDomain at a time; a workload of another
	// domain waits for the node like any other claim on its channel 0.
	It("holds another domain's workload on a busy node until the node is free", func(ctx SpecContext) {
		node := racks[0].nodes[0]
		applyComputeDomain(ctx, h, "other", 1, "Single")
		applyCDWorkload(ctx, h, "other-a", "other", node)
		Consistently(func(g Gomega) {
			g.Expect(cdWorkloadState(ctx, g, h, "other-a").scheduled()).To(BeFalse())
		}).WithContext(ctx).WithTimeout(cdHeldFor).WithPolling(config.PollInterval()).
			Should(Succeed(), "other-a must wait while rack-1 holds %s", node)

		deleteCDWorkload(ctx, h, "rack-1-c")
		waitCDWorkloadRunning(ctx, h, "other-a")
		other := getComputeDomain(ctx, Default, h, "other")
		label, _, err := h.Kube.NodeLabel(ctx, node, cdLabelKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(label).To(Equal(other.Metadata.UID), "%s now belongs to the other ComputeDomain", node)
	})

	// Fail open: a node without IMEX keeps simulating everything else, and the
	// ComputeDomain fails on it the way it fails on a real node without the
	// IMEX package.
	Context("on a node without IMEX", Ordered, func() {
		BeforeAll(func(ctx SpecContext) {
			deleteAllCDObjects(ctx, h)
			DeferCleanup(func(ctx SpecContext) {
				deleteAllCDObjects(ctx, h)
				Expect(h.Kube.Delete(ctx, gpuClaimTemplateManifest())).To(Succeed(), "delete the GPU claim template")
				prepareNodes(ctx, h, p, topo, racks, true)
			})
			// The DRA driver's GPU kubelet plugin adds nvidia-imex and
			// nvidia-imex-ctl, found when it started, to the CDI spec of every
			// claim it prepares; the claim keeps that spec after IMEX goes.
			Expect(h.Kube.Apply(ctx, gpuClaimTemplateManifest())).To(Succeed(), "apply the GPU claim template")
			Expect(h.Kube.Apply(ctx, gpuClaimPodManifest(racks[0].nodes[0]))).To(Succeed(), "apply %s", gpuClaimPod)
			waitCDWorkloadRunning(ctx, h, gpuClaimPod)
			res, err := h.Kube.ExecSh(ctx, kube.PodRef{Namespace: cdNamespace, Pod: gpuClaimPod}, "test -e /usr/bin/nvidia-imex-ctl")
			Expect(err).NotTo(HaveOccurred(), "the GPU claim's CDI spec mounts nvidia-imex-ctl: %s", res.Combined())

			prepareNodes(ctx, h, p, topo, racks, false)
		})

		// Withdrawing IMEX must not remove a bind source of a claim prepared
		// while it was staged, or the claim's container could not restart.
		It("restarts a GPU claim's container prepared while IMEX was staged", func(ctx SpecContext) {
			pod := kube.PodRef{Namespace: cdNamespace, Pod: gpuClaimPod}
			before := gpuClaimContainer(ctx, Default, h)
			res, err := h.Kube.ExecSh(ctx, pod, "kill 1")
			Expect(err).NotTo(HaveOccurred(), "stop the GPU claim's container: %s", res.Combined())

			Eventually(func(g Gomega) {
				status := gpuClaimContainer(ctx, g, h)
				g.Expect(status.RestartCount).To(BeNumerically(">", before.RestartCount), "the container restarted")
				g.Expect(status.Ready).To(BeTrue(), "the container runs again, waiting: %v", status.State.Waiting)
			}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
				Should(Succeed(), "%s restarts from its claim's CDI spec without IMEX", gpuClaimPod)
		})

		It("keeps the node agent ready and holds the workload in preparation", func(ctx SpecContext) {
			node := racks[0].nodes[0]
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			restarts := nodeAgentRestarts(ctx, h, node)
			applyComputeDomain(ctx, h, "no-imex", 1, "Single")
			applyCDWorkload(ctx, h, "no-imex-a", "no-imex", node)

			Eventually(func(g Gomega) {
				g.Expect(cdWorkloadEventReasons(ctx, g, h, "no-imex-a")).To(ContainElement("FailedPrepareDynamicResources"))
			}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
				Should(Succeed(), "kubelet reports the claim cannot be prepared")
			Consistently(func(g Gomega) {
				state := cdWorkloadState(ctx, g, h, "no-imex-a")
				g.Expect(state.scheduled()).To(BeTrue())
				g.Expect(state.Status.Phase).To(Equal("Pending"), "the workload stays in ContainerCreating")
				cd := getComputeDomain(ctx, g, h, "no-imex")
				g.Expect(cd.Status.Status).NotTo(Equal("Ready"))
				g.Expect(daemonNodes(ctx, g, h, cd.Metadata.UID, false)).To(ConsistOf(node), "the driver starts the IMEX daemon")
				g.Expect(daemonNodes(ctx, g, h, cd.Metadata.UID, true)).To(BeEmpty(), "the IMEX daemon never becomes ready")
			}).WithContext(ctx).WithTimeout(cdHeldFor).WithPolling(config.PollInterval()).
				Should(Succeed())
			assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
			// A ready DaemonSet also fits an agent that crash-looped and came
			// back; one that stayed ready never restarted.
			Expect(nodeAgentRestarts(ctx, h, node)).To(Equal(restarts), "the node agent on %s never restarted", node)
		})
	})
})

// nodeAgentRestarts reads both status lists: the node agent is a restartable
// init container in the chart's native-sidecar layout, the default with NRI on,
// and a regular container otherwise.
func nodeAgentRestarts(ctx context.Context, h *harness.Harness, node string) int {
	GinkgoHelper()
	pod := nvmlPodOnNode(ctx, h, node)
	out, err := h.Kube.KubectlCombined(ctx, "get", "pod", "-n", pod.Namespace, pod.Pod, "-o",
		`jsonpath={.status.initContainerStatuses[?(@.name=="node-agent")].restartCount}{.status.containerStatuses[?(@.name=="node-agent")].restartCount}`)
	Expect(err).NotTo(HaveOccurred(), "read the node agent's restart count on %s: %s", node, out)
	restarts, err := strconv.Atoi(strings.TrimSpace(out))
	Expect(err).NotTo(HaveOccurred(), "the node agent's restart count on %s: %q", node, out)
	return restarts
}

// discoverRacks groups the workers by their mokka.nvidia.com/clique label.
func discoverRacks(ctx context.Context, h *harness.Harness) []rack {
	GinkgoHelper()
	workers, err := h.Cluster.Workers(ctx)
	Expect(err).NotTo(HaveOccurred(), "list Kind workers")
	byClique := map[string][]string{}
	for _, w := range workers {
		clique, ok, err := h.Kube.NodeLabel(ctx, w.Name, rackLabel)
		Expect(err).NotTo(HaveOccurred(), "read %s on %s", rackLabel, w.Name)
		if ok && clique != "" {
			byClique[clique] = append(byClique[clique], w.Name)
		}
	}
	racks := make([]rack, 0, len(byClique))
	for clique, nodes := range byClique {
		slices.Sort(nodes)
		racks = append(racks, rack{clique: clique, nodes: nodes})
	}
	slices.SortFunc(racks, func(a, b rack) int { return strings.Compare(a.clique, b.clique) })
	return racks
}

func rackNodes(racks []rack) []string {
	var nodes []string
	for _, r := range racks {
		nodes = append(nodes, r.nodes...)
	}
	return nodes
}

// rackTopologyValues renders the chart's topology values from the racks, so
// the mock reports the clique each node is labeled with.
func rackTopologyValues(racks []rack) ([]byte, error) {
	type clique struct {
		id    int
		nodes []string
	}
	var uuids []string
	byUUID := map[string][]clique{}
	for _, r := range racks {
		cut := strings.LastIndex(r.clique, ".")
		if cut <= 0 {
			return nil, fmt.Errorf("%s %q is not <cluster UUID>.<clique ID>", rackLabel, r.clique)
		}
		id, err := strconv.Atoi(r.clique[cut+1:])
		if err != nil {
			return nil, fmt.Errorf("clique ID of %q: %w", r.clique, err)
		}
		uuid := r.clique[:cut]
		if _, seen := byUUID[uuid]; !seen {
			uuids = append(uuids, uuid)
		}
		byUUID[uuid] = append(byUUID[uuid], clique{id: id, nodes: r.nodes})
	}
	var b strings.Builder
	b.WriteString("# Generated by the ComputeDomain e2e scenario from the nodes' clique labels.\n")
	b.WriteString("topology:\n  enabled: true\n  domains:\n")
	for i, uuid := range uuids {
		fmt.Fprintf(&b, "    - name: rack-%d\n      uuid: %q\n      cliques:\n", i+1, uuid)
		for _, c := range byUUID[uuid] {
			fmt.Fprintf(&b, "        - id: %d\n          nodes:\n", c.id)
			for _, n := range c.nodes {
				fmt.Fprintf(&b, "            - %s\n", n)
			}
		}
	}
	return []byte(b.String()), nil
}

// installComputeDomainChart reinstalls nvml-mock with the rack topology. With
// imex the node agent stages IMEX, from E2E_IMEX_PAYLOAD_IMAGE when set; without
// it no source is configured and downloads are off, so it stages none.
func installComputeDomainChart(ctx context.Context, h *harness.Harness, p profile.Profile, topo string, imex bool) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	payload := config.IMEXPayloadImage()
	set := map[string]string{
		"gpu.count":                            strconv.Itoa(p.ExpectedGPUs()),
		"gpu.profile":                          p.Name,
		"image.repository":                     repo,
		"image.tag":                            tag,
		"nri.enabled":                          "true",
		`nodeSelector.mokka\.nvidia\.com/type`: sgpuNodeLabelValue,
		"updateStrategy.rollingUpdate.maxUnavailable": "100%",
		// A payload image is how CI provides IMEX, so it never downloads.
		"featureGates.IMEXDownload": strconv.FormatBool(imex && payload == ""),
	}
	if imex && payload != "" {
		payloadRepo, payloadTag := splitImage(payload)
		set["imex.payload.image.repository"] = payloadRepo
		set["imex.payload.image.tag"] = payloadTag
	}
	By(fmt.Sprintf("helm upgrade --install nvml-mock with the rack topology (profile=%s, IMEX=%t, payload=%q)", p.Name, imex, payload))
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		ValuesFiles:     []string{topo},
		Set:             set,
		Wait:            true,
		Timeout:         config.HelmTimeout(),
	})).To(Succeed(), "install nvml-mock for ComputeDomains")
}

// prepareNodes reinstalls nvml-mock with the rack topology, with or without
// IMEX, and waits until every node agent has staged both before restarting
// the DRA kubelet plugins, which read the clique and the IMEX binaries only
// when they start. The node pods restart too: a topology change alone does not
// roll them, and the agent would otherwise pick it up only at its next resync.
func prepareNodes(ctx SpecContext, h *harness.Harness, p profile.Profile, topo string, racks []rack, imex bool) {
	GinkgoHelper()
	installComputeDomainChart(ctx, h, p, topo, imex)
	nodes := rackNodes(racks)
	if !imex {
		// The node cache is a source too, and an earlier download may have
		// filled it, as one does with the IMEXDownload gate at its default.
		for _, node := range nodes {
			res, err := h.Kube.ExecSh(ctx, nvmlPodOnNode(ctx, h, node), "rm -rf "+nodeIMEXCache)
			Expect(err).NotTo(HaveOccurred(), "clear the IMEX node cache on %s: %s", node, res.Combined())
		}
	}
	Expect(h.Kube.DeletePodsByLabel(ctx, nvmlMockNamespace, nvmlMockSelector)).To(Succeed(), "restart the node pods")
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	waitIMEXStaged(ctx, h, nodes, imex)
	waitTopologyStaged(ctx, h, racks)
	restartDRAKubeletPlugins(ctx, h)
}

// waitTopologyStaged waits until every node agent has staged the rack topology
// beside the driver's config, where the kubelet plugin's mock NVML reads it.
func waitTopologyStaged(ctx context.Context, h *harness.Harness, racks []rack) {
	GinkgoHelper()
	for _, r := range racks {
		uuid := r.clique[:strings.LastIndex(r.clique, ".")]
		for _, node := range rackNodes(racks) {
			Eventually(func(g Gomega) {
				pod := nvmlPodOnNode(ctx, h, node)
				res, err := h.Kube.ExecSh(ctx, pod, "grep -qF "+uuid+" "+stagedTopology)
				g.Expect(err).NotTo(HaveOccurred(), "%s staged in %s on %s: %s", uuid, stagedTopology, node, res.Combined())
			}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
				Should(Succeed(), "the rack topology is staged on %s", node)
		}
	}
}

// waitIMEXStaged waits until the node agent on every node has staged IMEX, or,
// with staged false, has withdrawn it.
func waitIMEXStaged(ctx context.Context, h *harness.Harness, nodes []string, staged bool) {
	GinkgoHelper()
	check := "test -f " + stagedIMEX
	if !staged {
		check = "! test -e " + stagedIMEX
	}
	for _, node := range nodes {
		Eventually(func(g Gomega) {
			pod := nvmlPodOnNode(ctx, h, node)
			res, err := h.Kube.ExecSh(ctx, pod, check)
			g.Expect(err).NotTo(HaveOccurred(), "%s on %s: %s", check, node, res.Combined())
		}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
			Should(Succeed(), "IMEX staged=%t on %s", staged, node)
	}
}

// restartDRAKubeletPlugins makes the kubelet plugins read the node again: they
// take the clique from NVML and the IMEX binaries for the daemon's CDI edits
// from the driver root when they start. A rollout restart waits for every
// replacement pod, including those not created yet; waiting on the pods that
// exist at one moment can miss them.
func restartDRAKubeletPlugins(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	out, err := h.Kube.KubectlCombined(ctx, "get", "pods", "-n", draNamespace, "-l", draKubeletPluginSelector, "-o",
		`jsonpath={range .items[*]}{range .metadata.ownerReferences[?(@.controller==true)]}{.kind}/{.name}{"\n"}{end}{end}`)
	Expect(err).NotTo(HaveOccurred(), "find the DRA kubelet plugin DaemonSet: %s", out)
	owners := strings.Fields(out)
	slices.Sort(owners)
	owners = slices.Compact(owners)
	Expect(owners).To(HaveLen(1), "the DRA kubelet plugin pods have one owner")
	kind, name, _ := strings.Cut(owners[0], "/")
	Expect(kind).To(Equal("DaemonSet"), "the DRA kubelet plugin pods' owner")
	daemonSet := "daemonset/" + name

	out, err = h.Kube.KubectlCombined(ctx, "rollout", "restart", "-n", draNamespace, daemonSet)
	Expect(err).NotTo(HaveOccurred(), "restart the DRA kubelet plugins: %s", out)
	out, err = h.Kube.KubectlCombined(ctx, "rollout", "status", "-n", draNamespace, daemonSet,
		"--timeout="+config.ReadyTimeout().String())
	Expect(err).NotTo(HaveOccurred(), "wait for the DRA kubelet plugins: %s", out)
}

func cdNamespaceManifest() []byte {
	return []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + cdNamespace + "\n")
}

func computeDomainManifest(name string, numNodes int, allocationMode string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: resource.nvidia.com/v1beta1
kind: ComputeDomain
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  numNodes: %[3]d
  channel:
    resourceClaimTemplate:
      name: %[1]s-channel
    allocationMode: %[4]s
`, name, cdNamespace, numNodes, allocationMode))
}

func applyComputeDomain(ctx context.Context, h *harness.Harness, name string, numNodes int, allocationMode string) {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, computeDomainManifest(name, numNodes, allocationMode))).To(Succeed(), "apply ComputeDomain %s", name)
}

// cdWorkloadManifest renders a workload of a ComputeDomain pinned to node by
// hostname, as a user pins one.
func cdWorkloadManifest(name, domain, node string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  nodeSelector:
    kubernetes.io/hostname: %[4]s
  containers:
    - name: workload
      image: debian:bookworm-slim
      command: ["/bin/sh", "-c"]
      args: ["trap 'exit 0' TERM; sleep 3600 & wait"]
      resources:
        claims:
          - name: imex-channel
  resourceClaims:
    - name: imex-channel
      resourceClaimTemplateName: %[3]s-channel
`, name, cdNamespace, domain, node))
}

func applyCDWorkload(ctx context.Context, h *harness.Harness, name, domain, node string) {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, cdWorkloadManifest(name, domain, node))).To(Succeed(), "apply workload %s", name)
}

func deleteCDWorkload(ctx context.Context, h *harness.Harness, name string) {
	GinkgoHelper()
	_, err := h.Kube.KubectlCombined(ctx, "delete", "pod", "-n", cdNamespace, name, "--ignore-not-found", "--wait")
	Expect(err).NotTo(HaveOccurred(), "delete workload %s", name)
}

func deleteAllCDObjects(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	out, err := h.Kube.KubectlCombined(ctx, "delete", "pods,computedomains", "-n", cdNamespace, "--all", "--wait")
	Expect(err).NotTo(HaveOccurred(), "delete the ComputeDomain objects: %s", out)
}

func waitCDWorkloadRunning(ctx context.Context, h *harness.Harness, name string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(cdWorkloadState(ctx, g, h, name).Status.Phase).To(Equal("Running"))
	}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
		Should(Succeed(), "workload %s runs", name)
}

// cdChannels lists the IMEX channels a workload's container sees.
func cdChannels(ctx context.Context, h *harness.Harness, name string) []string {
	GinkgoHelper()
	res, err := h.Kube.ExecSh(ctx, kube.PodRef{Namespace: cdNamespace, Pod: name}, "ls "+cdChannelDir)
	Expect(err).NotTo(HaveOccurred(), "list %s in %s: %s", cdChannelDir, name, res.Combined())
	return strings.Fields(res.Stdout)
}

type cdPodState struct {
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

func (s cdPodState) scheduled() bool {
	for _, c := range s.Status.Conditions {
		if c.Type == "PodScheduled" {
			return c.Status == "True"
		}
	}
	return false
}

func cdWorkloadState(ctx context.Context, g Gomega, h *harness.Harness, name string) cdPodState {
	var state cdPodState
	g.Expect(h.Kube.GetJSON(ctx, &state, "pod", "-n", cdNamespace, name)).To(Succeed())
	return state
}

func cdWorkloadEventReasons(ctx context.Context, g Gomega, h *harness.Harness, name string) []string {
	var events struct {
		Items []struct {
			Reason string `json:"reason"`
		} `json:"items"`
	}
	g.Expect(h.Kube.GetJSON(ctx, &events, "events", "-n", cdNamespace,
		"--field-selector", "involvedObject.kind=Pod,involvedObject.name="+name)).To(Succeed())
	reasons := make([]string, 0, len(events.Items))
	for _, e := range events.Items {
		reasons = append(reasons, e.Reason)
	}
	return reasons
}

type computeDomain struct {
	Metadata struct {
		UID string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		Status string `json:"status"`
		Nodes  []struct {
			Name     string `json:"name"`
			CliqueID string `json:"cliqueID"`
		} `json:"nodes"`
	} `json:"status"`
}

func (cd computeDomain) nodeNames() []string {
	names := make([]string, 0, len(cd.Status.Nodes))
	for _, n := range cd.Status.Nodes {
		names = append(names, n.Name)
	}
	return names
}

// cliques lists the distinct cliques of the domain's nodes.
func (cd computeDomain) cliques() []string {
	var cliques []string
	for _, n := range cd.Status.Nodes {
		if !slices.Contains(cliques, n.CliqueID) {
			cliques = append(cliques, n.CliqueID)
		}
	}
	return cliques
}

func getComputeDomain(ctx context.Context, g Gomega, h *harness.Harness, name string) computeDomain {
	var cd computeDomain
	g.Expect(h.Kube.GetJSON(ctx, &cd, "computedomain", "-n", cdNamespace, name)).To(Succeed())
	return cd
}

// daemonNodes lists the nodes running an IMEX daemon pod of the domain, or,
// with ready, only those where it is ready.
func daemonNodes(ctx context.Context, g Gomega, h *harness.Harness, uid string, ready bool) []string {
	var pods struct {
		Items []struct {
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				ContainerStatuses []struct {
					Ready bool `json:"ready"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	g.Expect(h.Kube.GetJSON(ctx, &pods, "pods", "-n", draNamespace, "-l", cdLabelKey+"="+uid)).To(Succeed())
	var nodes []string
	for _, pod := range pods.Items {
		podReady := len(pod.Status.ContainerStatuses) > 0
		for _, c := range pod.Status.ContainerStatuses {
			podReady = podReady && c.Ready
		}
		if podReady || !ready {
			nodes = append(nodes, pod.Spec.NodeName)
		}
	}
	return nodes
}

func collectComputeDomainOnFailure(ctx context.Context, h *harness.Harness) {
	if !CurrentSpecReport().Failed() || h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, "compute-domain")
	c.Kubectl(ctx, "computedomains.yaml", "get", "computedomains", "-A", "-o", "yaml")
	c.Kubectl(ctx, "computedomaincliques.yaml", "get", "computedomaincliques", "-A", "-o", "yaml")
	c.Kubectl(ctx, "workloads.txt", "describe", "pods", "-n", cdNamespace)
	c.Kubectl(ctx, "resourceclaims.yaml", "get", "resourceclaims", "-n", cdNamespace, "-o", "yaml")
	c.Kubectl(ctx, "dra-pods.txt", "get", "pods", "-n", draNamespace, "-o", "wide")
	c.Kubectl(ctx, "dra-daemons.txt", "logs", "-n", draNamespace, "-l", cdLabelKey, "--all-containers", "--tail=200")
	c.Kubectl(ctx, "dra-kubelet-plugins.txt", "logs", "-n", draNamespace, "-l", draKubeletPluginSelector,
		"-c", "compute-domains", "--tail=200")
	c.Kubectl(ctx, "nodes.txt", "get", "nodes", "--show-labels")
	c.Kubectl(ctx, "node-agents.txt", "logs", "-n", nvmlMockNamespace, "-l", nvmlMockSelector, "-c", "node-agent", "--tail=100")
}

func gpuClaimTemplateManifest() []byte {
	return []byte(fmt.Sprintf(`apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: gpu-claim
  namespace: %s
spec:
  spec:
    devices:
      requests:
        - name: gpu
          deviceClassName: gpu.nvidia.com
`, cdNamespace))
}

// gpuClaimPodManifest restarts its container on exit, so stopping it shows
// whether kubelet can recreate the container from the claim's CDI spec.
func gpuClaimPodManifest(node string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  restartPolicy: Always
  terminationGracePeriodSeconds: 1
  nodeSelector:
    kubernetes.io/hostname: %[3]s
  containers:
    - name: app
      image: debian:bookworm-slim
      command: ["/bin/sh", "-c"]
      args: ["trap 'exit 0' TERM; sleep 3600 & wait"]
      resources:
        claims:
          - name: gpu
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: gpu-claim
`, gpuClaimPod, cdNamespace, node))
}

type gpuClaimContainerStatus struct {
	RestartCount int  `json:"restartCount"`
	Ready        bool `json:"ready"`
	State        struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting"`
	} `json:"state"`
}

func gpuClaimContainer(ctx context.Context, g Gomega, h *harness.Harness) gpuClaimContainerStatus {
	var pod struct {
		Status struct {
			ContainerStatuses []gpuClaimContainerStatus `json:"containerStatuses"`
		} `json:"status"`
	}
	g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", "-n", cdNamespace, gpuClaimPod)).To(Succeed(), "read %s", gpuClaimPod)
	g.Expect(pod.Status.ContainerStatuses).To(HaveLen(1), "%s has one container status", gpuClaimPod)
	return pod.Status.ContainerStatuses[0]
}
