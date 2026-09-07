//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

const (
	networkOperatorNamespace = "nvidia-network-operator"
	networkOperatorRelease   = "network-operator"
	networkOperatorRepo      = "nvidia-netop"
	networkOperatorChart     = "nvidia-netop/network-operator"
	networkOperatorRepoURL   = "https://helm.ngc.nvidia.com/nvidia"
	// Pinned to the chart whose NFD wiring and rdmaSharedDevicePlugin
	// reconciliation this scenario was written against.
	networkOperatorVersion = "26.7.0"

	// The label the Network Operator selects Mellanox nodes on. Its bundled NFD
	// cannot derive it from the pci source — that source is configured for GPU
	// device classes (0300/0302) only, and an HCA is class 0207 — so it comes
	// from NFD's local source reading the feature file the node agent writes
	// (internal/agent/pcibus).
	pciMellanoxLabel = "feature.node.kubernetes.io/pci-15b3.present"

	// The DaemonSet the operator reconciles out of the NicClusterPolicy's
	// rdmaSharedDevicePlugin section, into its own namespace.
	rdmaPluginDaemonSet = "rdma-shared-dp-ds"

	// rdmaHcaMax from the NicClusterPolicy asset, and so the capacity the
	// plugin registers. Not one unit per HCA: an RDMA device is shared, and the
	// resource meters how many containers may hold it concurrently.
	rdmaHcaMax = 63

	// Longer than the shared HelmTimeout: --wait here covers the operator, an
	// NFD master, a garbage collector and a worker per node, so the release
	// becomes ready at the pace of four cold image pulls rather than one.
	networkOperatorInstallTimeout = 10 * time.Minute

	// Reconciling the CR and pulling the plugin image dominate this; the
	// registration itself is one kubelet patch away once the plugin is up.
	rdmaCapacityTimeout = 5 * time.Minute
	rdmaCapacityPoll    = 5 * time.Second
)

// Proves that a real RDMA consumer — the NVIDIA Network Operator and the
// k8s-rdma-shared-dev-plugin it deploys — discovers the simulated HCAs and
// turns them into schedulable rdma/ib capacity. Nothing in nvml-mock speaks to
// that plugin: it reads the mock InfiniBand and Mellanox PCI sysfs trees the
// node agent renders and the NRI plugin serves at the kernel paths, which is
// the entire claim under test.
//
// Needs a node kernel that has the RDMA subsystem registered (ib_core loaded).
// The plugin asks the kernel for the RDMA network-namespace mode before it
// looks at any device and exits when that netlink family has no listener; a
// sysfs mock cannot stand in for a kernel subsystem. The e2e-rdma CI job loads
// the module before the cluster is created.
//
// Ordered because the absent-check is only meaningful before the plugin exists;
// ContinueOnFailure because the specs report independent facts (no resource,
// the vendor label, the capacity) and a plain Ordered container would stop at
// the first failure and hide the rest.
var _ = Describe("nvml-mock RDMA resource advertisement", Label("rdma"), Ordered, ContinueOnFailure, func() {
	var (
		h    *harness.Harness
		node string
	)

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "rdma")
		assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())

		// The worker a mock pod actually runs on, never FirstNodeName: on the
		// default kind config that is the control plane, where the operator
		// schedules no plugin — which would make the absent-check pass for the
		// wrong reason and the capacity check fail forever.
		_, node = nvmlMockPodOnWorker(ctx, h)
	})

	It("advertises no rdma/ib before the device plugin exists", Label("rdma-provenance"), func(ctx SpecContext) {
		// Ordered by BeforeAll's WaitDaemonSetReady: the mock is already
		// serving, so this cannot pass merely because nothing has rendered yet.
		assertions.ResourceAbsent(ctx, h.Kube, node, kube.RDMAResourceName)
	})

	It("labels the node with the PCI vendor the operator selects on", Label("rdma-provenance"), func(ctx SpecContext) {
		Expect(h.Helm.RepoAdd(ctx, networkOperatorRepo, networkOperatorRepoURL)).To(Succeed(), "add Network Operator Helm repo")
		Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
			Name:            networkOperatorRelease,
			Chart:           networkOperatorChart,
			Version:         networkOperatorVersion,
			Namespace:       networkOperatorNamespace,
			CreateNamespace: true,
			Wait:            true,
			Timeout:         networkOperatorInstallTimeout,
		})).To(Succeed(), "install Network Operator")

		assertions.WaitNodeLabelsPresent(ctx, h.Kube, node,
			[]string{pciMellanoxLabel}, rdmaCapacityTimeout, rdmaCapacityPoll)
		assertions.NodeLabelEquals(ctx, h.Kube, node, pciMellanoxLabel, "true")
	})

	It("advertises rdma/ib once the device plugin discovers the mock HCAs", Label("rdma-capacity"), func(ctx SpecContext) {
		Expect(h.Kube.Apply(ctx, assets.NicClusterPolicyManifest)).To(Succeed(), "apply NicClusterPolicy")

		// Readiness is asserted before the capacity: the plugin exits rather
		// than degrades when the kernel or the sysfs trees do not satisfy it,
		// so a CrashLoopBackOff here says "the mock was not enough" while a
		// missing capacity with a ready plugin says "discovery found nothing".
		assertions.WaitDaemonSetReady(ctx, h.Kube, networkOperatorNamespace, rdmaPluginDaemonSet,
			rdmaCapacityTimeout, rdmaCapacityPoll)
		assertions.WaitCapacityResource(ctx, h.Kube, node, kube.RDMAResourceName, rdmaHcaMax,
			rdmaCapacityTimeout, rdmaCapacityPoll)
	})
})
