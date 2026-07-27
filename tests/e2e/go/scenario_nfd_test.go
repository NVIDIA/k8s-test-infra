//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

const (
	nfdNamespace = "node-feature-discovery"
	nfdRelease   = "nfd"
	nfdChart     = "nfd/node-feature-discovery"
	nfdRepoURL   = "https://kubernetes-sigs.github.io/node-feature-discovery/charts"
	// Pinned to the version in go.mod. Every upstream behaviour this test
	// relies on (local-source featureFilesDir, label namespacing) was read
	// from this tag.
	nfdVersion = "0.19.0"

	// The label under test. NFD's local source turns a features.d line
	// "pci-10de.present=true" into exactly this key.
	pciVendorLabel = "feature.node.kubernetes.io/pci-10de.present"

	// Container path of the feature file setup.sh writes (chart mount from
	// Task 2; the hostPath behind it is nodeLabels.featuresDir).
	nfdFeatureFile = "/host/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features"

	nfdLabelTimeout = 3 * time.Minute
	nfdLabelPoll    = 5 * time.Second
)

// Proves provenance of the NFD PCI vendor label by ordering, not inspection:
// with NFD absent the label must not exist (nothing in nvml-mock may write
// it), and once NFD is installed it must appear. See issue #505.
//
// Ordered because the absent-check is only meaningful before NFD is installed;
// ContinueOnFailure because the three specs report independent facts (no label,
// the feature file, the label after NFD) and a plain Ordered container stops at
// the first failure, hiding the other two.
var _ = Describe("nvml-mock NFD label provenance", Label("nfd"), Ordered, ContinueOnFailure, func() {
	var (
		h    *harness.Harness
		pod  kube.PodRef
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "nvml-mock-nfd", demoKindConfig([]string{"a100"}), "nfd")
		installDemoChart(ctx, h, "a100", 8)

		// Derive the node from where an nvml-mock pod actually runs — never
		// h.Kube.FirstNodeName, which returns Items[0]: on the demo kind
		// config that is the CONTROL PLANE, and the NFD chart schedules no
		// worker there (no control-plane toleration). Targeting it would make
		// the absent-check pass for the wrong reason and the present-check
		// fail forever. Measured in the Task 1 experiment.
		// Using the mock pod's node also guarantees the feature file and the
		// label assertion refer to the same machine.
		pod, node = nvmlMockPodOnWorker(ctx, h)
		Expect(node).NotTo(ContainSubstring("control-plane"),
			"nvml-mock pod landed on the control plane; NFD runs no worker there")
	})

	It("does not create the PCI vendor label without NFD", Label("nfd-provenance"), func(ctx SpecContext) {
		assertions.NodeLabelAbsent(ctx, h.Kube, node, pciVendorLabel)
	})

	It("writes the feature file NFD's local source reads", Label("nfd-provenance"), func(ctx SpecContext) {
		res, err := h.Kube.ExecSh(ctx, pod, "cat "+nfdFeatureFile)
		Expect(err).NotTo(HaveOccurred(), "reading %s: %s", nfdFeatureFile, res.Combined())
		Expect(strings.TrimSpace(res.Stdout)).To(Equal("pci-10de.present=true"),
			"feature file contents drive the label NFD creates")
	})

	It("gets the label from NFD once NFD is installed", Label("nfd-provenance"), func(ctx SpecContext) {
		Expect(h.Helm.RepoAdd(ctx, "nfd", nfdRepoURL)).To(Succeed(), "add NFD Helm repo")
		Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
			Name:            nfdRelease,
			Chart:           nfdChart,
			Version:         nfdVersion,
			Namespace:       nfdNamespace,
			CreateNamespace: true,
			Wait:            true,
			Timeout:         config.HelmTimeout(),
		})).To(Succeed(), "install NFD")

		assertions.WaitNodeLabelsPresent(ctx, h.Kube, node,
			[]string{pciVendorLabel}, nfdLabelTimeout, nfdLabelPoll)
		assertions.NodeLabelEquals(ctx, h.Kube, node, pciVendorLabel, "true")
	})
})

// nvmlMockPodOnWorker returns a running nvml-mock pod scheduled on a Kind
// WORKER node, plus that node's name.
//
// The pod is selected by node role, never by list position. The chart's default
// toleration is `operator: Exists`, so the mock DaemonSet also lands on the
// control plane; the API returns pods in name order and DaemonSet pod names
// carry a random suffix, so a positional pick (FirstPodName -> Items[0]) hits
// the control-plane pod about one run in four — and the NFD chart schedules no
// worker there, which would make this whole scenario fail for a reason that has
// nothing to do with the label. Names are sorted so repeated runs on the same
// cluster agree on which worker they target.
func nvmlMockPodOnWorker(ctx context.Context, h *harness.Harness) (kube.PodRef, string) {
	GinkgoHelper()
	workers, err := h.Cluster.Workers(ctx)
	Expect(err).NotTo(HaveOccurred(), "list Kind worker nodes")
	Expect(workers).NotTo(BeEmpty(), "NFD label provenance needs at least one Kind worker node")
	isWorker := make(map[string]bool, len(workers))
	for _, w := range workers {
		isWorker[w.Name] = true
	}

	var (
		ref  kube.PodRef
		node string
	)
	Eventually(func() (string, error) {
		names, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, nvmlMockSelector)
		if err != nil {
			return "", err
		}
		sort.Strings(names)
		for _, name := range names {
			n, err := h.Kube.PodNode(ctx, nvmlMockNamespace, name)
			if err != nil {
				return "", err
			}
			if isWorker[n] {
				ref = kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
				node = n
				return n, nil
			}
		}
		return "", nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running nvml-mock pod on a Kind worker node")
	return ref, node
}
