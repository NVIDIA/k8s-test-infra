//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
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
	imexClusterName    = "nvml-mock-imex"
	imexWorkloadNS     = "default"
	imexAgentDaemonSet = "imex-agent"
	imexAgentSelector  = "app=imex-agent"
	imexChannelCount   = 16
	imexChannelDir     = "/dev/nvidia-caps-imex-channels"
)

// Proves mock IMEX channel injection over NRI: a plain workload annotated
// nvml-mock.nvidia.com/imex-channels="true" sees imexChannelCount channel
// device nodes on every worker, with consistent ComputeDomain identity. The
// real nvidia-imex domain-status check is demo-only (see docs/demo/nri-imex),
// so it is intentionally absent here to keep e2e hermetic.
//
// This scenario depends on the NRI plugin (it installs with nri.enabled=true on
// an NRI-enabled Kind cluster), so it carries the shared "nri" label: the
// default `make e2e` filter excludes it via `!nri`, and it runs only when NRI is
// enabled (`make e2e-nri` / --label-filter=nri). The "imex" label still selects
// it on its own (--label-filter=imex).
var _ = Describe("nvml-mock NRI IMEX channel injection", Label("imex", "nri"), Ordered, func() {
	var (
		h          *harness.Harness
		workers    []cluster.Node
		topoValues string
	)
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, imexClusterName, assets.KindNRIConfig, "imex")
		var err error
		workers, err = h.Cluster.Workers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(workers)).To(BeNumerically(">=", 2),
			"imex scenario needs >= 2 Kind workers, found %d", len(workers))
		topoValues = writeNRITopologyValues(workers)
		DeferCleanup(func() { _ = os.Remove(topoValues) })
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var p profile.Profile

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				installImexChart(ctx, h, p, topoValues, p.HasFabric())
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, nriNRIDaemonSet, config.ReadyTimeout(), config.PollInterval())
				deployImexAgent(ctx, h)
			})

			It("injects IMEX channel device nodes on every worker", Label("imex-channels"), func(ctx SpecContext) {
				assertWorkloadSeesImexChannels(ctx, h, workers, imexChannelCount)
			})

			It("carries consistent ComputeDomain identity across nodes", Label("compute-domain"), func(ctx SpecContext) {
				if !p.HasFabric() {
					Skip("profile " + name + " declares no device_defaults.fabric block")
				}
				assertNodeCliqueIdentities(ctx, h, imexWorkloadNS, imexAgentSelector, workers)
			})
		})
	}
})

// installImexChart installs nvml-mock with NRI + IMEX channels enabled.
func installImexChart(ctx context.Context, h *harness.Harness, p profile.Profile, topoValues string, withComputeDomain bool) {
	GinkgoHelper()
	repo, tag := splitImage(config.Image())
	rel := helmReleaseForImex(p, repo, tag)
	if withComputeDomain {
		rel.ValuesFiles = []string{topoValues}
	}
	By("helm upgrade --install nvml-mock with NRI + imexChannels (profile=" + p.Name + ")")
	Expect(h.Helm.UpgradeInstall(ctx, rel)).To(Succeed(), "helm upgrade --install nvml-mock imex (profile=%s)", p.Name)
}

// helmReleaseForImex mirrors installNRIChart's release struct but adds the
// imexChannels.count override that enables mock IMEX channel injection.
func helmReleaseForImex(p profile.Profile, repo, tag string) helm.Release {
	return helm.Release{
		Name:            "nvml-mock",
		Chart:           chartDir(),
		Namespace:       nvmlMockNamespace,
		CreateNamespace: true,
		HideOutput:      true,
		Set: map[string]string{
			"gpu.count":          strconv.Itoa(p.ExpectedGPUs()),
			"gpu.profile":        p.Name,
			"image.repository":   repo,
			"image.tag":          tag,
			"nri.enabled":        "true",
			"imexChannels.count": strconv.Itoa(imexChannelCount),
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	}
}

// deployImexAgent (re)creates the annotated workload AFTER overlay staging, so
// NRI (which only injects at container-creation time) sees the staged channels.
func deployImexAgent(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Kube.Delete(ctx, assets.ImexWorkloadManifest)).To(Succeed(), "delete previous imex-agent DaemonSet")
	Expect(h.Kube.Apply(ctx, assets.ImexWorkloadManifest)).To(Succeed(), "apply imex-agent DaemonSet")
	assertions.WaitDaemonSetReady(ctx, h.Kube, imexWorkloadNS, imexAgentDaemonSet, config.ReadyTimeout(), config.PollInterval())
}

// assertWorkloadSeesImexChannels execs `ls` in the agent pod on each worker and
// asserts exactly expected channel device nodes are present.
func assertWorkloadSeesImexChannels(ctx context.Context, h *harness.Harness, workers []cluster.Node, expected int) {
	GinkgoHelper()
	for _, w := range workers {
		pod := agentPodOnNode(ctx, h, imexWorkloadNS, imexAgentSelector, w.Name)
		res, err := h.Kube.ExecSh(ctx, pod, "ls -1 "+imexChannelDir)
		Expect(err).NotTo(HaveOccurred(), "ls %s on %s: %s", imexChannelDir, w.Name, res.Combined())
		Expect(countChannelLines(res.Combined())).To(Equal(expected),
			"%s: expected %d IMEX channels\n%s", w.Name, expected, strings.TrimSpace(res.Combined()))
	}
}

// agentPodOnNode locates a running agent pod on the given node by namespace and
// label selector, so multiple scenarios (gpu-agent, imex-agent) can share it.
func agentPodOnNode(ctx context.Context, h *harness.Harness, ns, selector, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		pods, err := h.Kube.RunningPodNames(ctx, ns, selector)
		if err != nil {
			return "", err
		}
		for _, pod := range pods {
			podNode, err := h.Kube.PodNode(ctx, ns, pod)
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
		ShouldNot(BeEmpty(), "no running %s pod on node %s", selector, node)
	return kube.PodRef{Namespace: ns, Pod: name}
}

func countChannelLines(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "channel") {
			count++
		}
	}
	return count
}
