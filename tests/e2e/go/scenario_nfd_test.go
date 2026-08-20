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

	// NFD records every label it owns in this annotation, as a comma-separated
	// list of label names WITHOUT the feature.node.kubernetes.io/ prefix (its
	// default namespace). A label written by anything else — setup.sh's
	// `kubectl label`, say — never appears here, which makes this the direct
	// ownership discriminator rather than an inference from ordering. Measured
	// in the Task 1 experiment.
	nfdOwnedLabelsAnnotation = "nfd.node.kubernetes.io/feature-labels"
	pciVendorFeature         = "pci-10de.present"

	// Written unconditionally by setup.sh in step 7, before anything
	// pci-10de-related, and used here only as a synchronisation barrier.
	// See spec 1.
	gpuPresentLabel = "nvidia.com/gpu.present"

	// Container path of the feature file setup.sh writes (chart mount from
	// Task 2; the hostPath behind it is nodeLabels.featuresDir).
	nfdFeatureFile = "/host/etc/kubernetes/node-feature-discovery/features.d/nvml-mock.features"

	nfdLabelTimeout = 3 * time.Minute
	nfdLabelPoll    = 5 * time.Second
)

// Proves provenance of the NFD PCI vendor label two independent ways: by
// ordering (with NFD absent the label must not exist, because nothing in
// nvml-mock may write it, and it must appear once NFD is installed) and by
// ownership (NFD lists the label in its own feature-labels annotation, which a
// hand-written label never reaches). Either one alone can be fooled — the
// ordering half by a race, the ownership half by an NFD version that stops
// annotating — so both are asserted. See issue #505.
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
		h = setupCluster(ctx, "nfd")
		assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())

		// Derive the node from where an nvml-mock pod actually runs — never
		// h.Kube.FirstNodeName, which returns Items[0]: on the demo kind
		// config that is the CONTROL PLANE, and the NFD chart schedules no
		// worker there (no control-plane toleration). Targeting it would make
		// the absent-check pass for the wrong reason and the present-check
		// fail forever. Measured in the Task 1 experiment.
		// Using the mock pod's node also guarantees the feature file and the
		// label assertion refer to the same machine. The worker-role invariant
		// is enforced inside that helper, by Kind's own node classification.
		pod, node = nvmlMockPodOnWorker(ctx, h)
	})

	It("does not create the PCI vendor label without NFD", Label("nfd-provenance"), func(ctx SpecContext) {
		// SYNCHRONISATION BARRIER — not a feature assertion. Do not "tidy" it
		// away: without it this negative check is unordered against setup.sh
		// and can pass vacuously, which would make the whole guard useless.
		//
		// Nothing else orders us after setup.sh's labelling step: the nvml-mock
		// DaemonSet declares no readinessProbe, demoRelease sets
		// maxUnavailable=100% so `helm --wait` returns on merely-scheduled
		// pods, and pod selection waits only for phase Running. setup.sh writes
		// nvidia.com/gpu.present unconditionally in step 7, still ahead of the
		// pci-10de feature-file block that follows it, so observing it proves
		// setup.sh reached the labelling block — and that a still-absent
		// pci-10de label is a real absence, not a race we won.
		assertions.WaitNodeLabelsPresent(ctx, h.Kube, node,
			[]string{gpuPresentLabel}, nfdLabelTimeout, nfdLabelPoll)

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

		// Presence alone cannot tell "NFD derived it" from "setup.sh wrote it",
		// so assert ownership directly: NFD patches the label and this
		// annotation in the same node update, so once the label is observed the
		// annotation is too — no second wait needed.
		assertions.NodeAnnotationListContains(ctx, h.Kube, node,
			nfdOwnedLabelsAnnotation, pciVendorFeature)
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
