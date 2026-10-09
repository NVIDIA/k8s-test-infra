//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/pod"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	operatorDevicePluginDS        = "nvidia-device-plugin-daemonset"
	operatorDevicePluginContainer = "nvidia-device-plugin"
	operatorGFDDS                 = "gpu-feature-discovery"

	// operatorProcMigMinors is where the device plugin opens the capability
	// table: the path is hardcoded upstream, so the mock can only serve it
	// there, through the CDI hook, and not at the driver root.
	operatorProcMigMinors = "/proc/driver/nvidia-caps/mig-minors"
)

// migMixedLayouts puts one slice of each of the three smallest sizes on every
// GPU, so migStrategy=mixed has three resource names to publish. Six of seven
// slices is a placement every board accepts. Like migLayouts, every
// MIG-capable profile needs an entry, and the names are that board's own, read
// off `nvidia-smi mig -lgip`.
var migMixedLayouts = map[string][]migLayout{
	"a100":  {{Profile: "1g.5gb", Count: 1}, {Profile: "2g.10gb", Count: 1}, {Profile: "3g.20gb", Count: 1}},
	"h100":  {{Profile: "1g.10gb", Count: 1}, {Profile: "2g.20gb", Count: 1}, {Profile: "3g.40gb", Count: 1}},
	"b200":  {{Profile: "1g.23gb", Count: 1}, {Profile: "2g.45gb", Count: 1}, {Profile: "3g.90gb", Count: 1}},
	"gb200": {{Profile: "1g.23gb", Count: 1}, {Profile: "2g.47gb", Count: 1}, {Profile: "3g.93gb", Count: 1}},
	"gb300": {{Profile: "1g.35gb", Count: 1}, {Profile: "2g.70gb", Count: 1}, {Profile: "3g.139gb", Count: 1}},
}

// GPU Operator MIG scenario. The MIG scenario proves the upstream device plugin
// publishes partitions when it is deployed by hand with the capability table
// mounted in. This one proves the same with nothing hand-deployed: the GPU
// Operator's own, stock device plugin and GFD, which no manifest of ours can
// mount into, receive the table through the CDI spec the node-agent writes.
//
// It attaches to a cluster Tilt provisioned with --gpu-operator and reshapes
// it: nvml-mock is reinstalled partitioned, and the ClusterPolicy's
// mig.strategy is switched to match, which is how an operator user would turn
// MIG on. Each strategy then has to show up in what the node advertises, what
// GFD labels, and what a pod is handed.
var _ = Describe("nvml-mock GPU Operator MIG", Label("gpu-operator-mig"), Ordered, func() {
	var h *harness.Harness

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "gpu-operator-mig")
	})

	for _, name := range config.SelectedProfileNames() {
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p      profile.Profile
				target cluster.Node
			)

			BeforeAll(func(ctx SpecContext) {
				p = loadProfile(name)
				if !p.MIGCapable() {
					Skip(fmt.Sprintf("profile %s is not MIG-capable", name))
				}
				target = gpuOperatorTargetNode(ctx, h)
				waitOperatorValidatorRunning(ctx, h)
			})

			Context("migStrategy=single", Ordered, func() {
				var layout migLayout

				BeforeAll(func(ctx SpecContext) {
					var ok bool
					layout, ok = migLayouts[name]
					Expect(ok).To(BeTrue(), "MIG-capable profile %s has no entry in migLayouts", name)
					reconfigureOperatorMIG(ctx, h, p, target, []migLayout{layout}, "single")
				})

				It("advertises one nvidia.com/gpu per partition from the stock device plugin", func(ctx SpecContext) {
					assertions.WaitAllocatableGPU(ctx, h.Kube, target.Name, p.ExpectedGPUs()*layout.Count,
						config.OperandSettleTimeout(), config.PollInterval())
				})

				It("serves the staged capability table to the plugin at /proc/driver", func(ctx SpecContext) {
					expectOperandServedMIGCaps(ctx, h, target.Name)
				})

				It("labels the node as a pool of identical partitions", func(ctx SpecContext) {
					assertions.WaitGFDLabels(ctx, h.Kube, target.Name, map[string]string{
						"nvidia.com/mig.strategy":  "single",
						assertions.GFDLabelProduct: p.GFDProductName() + "-MIG-" + layout.Profile,
						assertions.GFDLabelCount:   strconv.Itoa(p.ExpectedGPUs() * layout.Count),
						"nvidia.com/gpu.slices.gi": strconv.Itoa(migSlices(layout.Profile)),
						"nvidia.com/mig.capable":   "true",
					}, config.OperandSettleTimeout(), config.PollInterval())
				})

				It("hands a pod exactly one partition", func(ctx SpecContext) {
					expectPodGetsOnePartition(ctx, h, target.Name, "gpu-operator-mig-single", kube.GPUResourceName)
				})
			})

			Context("migStrategy=mixed", Ordered, func() {
				var layouts []migLayout

				BeforeAll(func(ctx SpecContext) {
					var ok bool
					layouts, ok = migMixedLayouts[name]
					Expect(ok).To(BeTrue(), "MIG-capable profile %s has no entry in migMixedLayouts", name)
					reconfigureOperatorMIG(ctx, h, p, target, layouts, "mixed")
				})

				It("advertises one resource per slice profile and no whole GPUs", func(ctx SpecContext) {
					for _, l := range layouts {
						assertions.WaitAllocatable(ctx, h.Kube, target.Name, migResource(l.Profile), p.ExpectedGPUs()*l.Count,
							config.OperandSettleTimeout(), config.PollInterval())
					}
					assertions.WaitAllocatableGPU(ctx, h.Kube, target.Name, 0, config.ReadyTimeout(), config.PollInterval())
				})

				It("serves the staged capability table to the plugin at /proc/driver", func(ctx SpecContext) {
					expectOperandServedMIGCaps(ctx, h, target.Name)
				})

				It("labels each slice profile separately", func(ctx SpecContext) {
					want := map[string]string{
						"nvidia.com/mig.strategy":  "mixed",
						assertions.GFDLabelProduct: p.GFDProductName(),
						assertions.GFDLabelCount:   strconv.Itoa(p.ExpectedGPUs()),
					}
					for _, l := range layouts {
						prefix := "nvidia.com/mig-" + l.Profile
						want[prefix+".count"] = strconv.Itoa(p.ExpectedGPUs() * l.Count)
						want[prefix+".slices.gi"] = strconv.Itoa(migSlices(l.Profile))
						want[prefix+".product"] = p.GFDProductName() + "-MIG-" + l.Profile
					}
					assertions.WaitGFDLabels(ctx, h.Kube, target.Name, want,
						config.OperandSettleTimeout(), config.PollInterval())
				})

				It("hands a pod exactly one partition of the profile it asked for", func(ctx SpecContext) {
					largest := layouts[len(layouts)-1]
					expectPodGetsOnePartition(ctx, h, target.Name, "gpu-operator-mig-mixed", migResource(largest.Profile))
				})
			})

			// Negative control, and the restore: the hook is gated on the GPUs
			// being partitioned, so with MIG off the plugin must see the node's
			// own /proc/driver and advertise whole GPUs again.
			Context("MIG disabled", Ordered, func() {
				BeforeAll(func(ctx SpecContext) {
					reconfigureOperatorMIG(ctx, h, p, target, nil, "none")
				})

				It("advertises whole GPUs and leaves /proc/driver alone", func(ctx SpecContext) {
					assertions.WaitAllocatableGPU(ctx, h.Kube, target.Name, p.ExpectedGPUs(),
						config.OperandSettleTimeout(), config.PollInterval())
					plugin := operandPodRef(ctx, h, operatorDevicePluginDS, operatorDevicePluginContainer, target.Name)
					_, err := h.Kube.ExecSh(ctx, plugin, "test ! -e "+operatorProcMigMinors)
					Expect(err).NotTo(HaveOccurred(), "%s visible to the device plugin with MIG disabled", operatorProcMigMinors)
				})
			})
		})
	}
})

// reconfigureOperatorMIG reinstalls nvml-mock with layouts, points the
// ClusterPolicy at strategy, and restarts the two operands that read MIG
// state, returning once they run against the new layout. An empty layouts
// installs with MIG disabled.
//
// The restart is not optional. Both operands read the partition layout once at
// startup, and the CDI edits carrying the capability table apply at container
// creation, so an operand that outlived the reinstall keeps serving the layout,
// and the /proc/driver, it started with.
func reconfigureOperatorMIG(ctx SpecContext, h *harness.Harness, p profile.Profile, target cluster.Node, layouts []migLayout, strategy string) {
	GinkgoHelper()
	installMIGLayouts(ctx, h, p, layouts, len(layouts) > 0)

	// helm --wait covers the DaemonSet rollout, not the node-agent rewriting
	// the CDI spec after it. An operand restarted before the rewrite would be
	// created from the previous spec, hook and all.
	grep := "grep -q " + procDriverRelPathInSpec + " /var/run/cdi/nvidia.yaml"
	if len(layouts) == 0 {
		grep = "! " + grep
	}
	By(fmt.Sprintf("waiting for the CDI spec on %s to match gpu.mig.enabled=%t", target.Name, len(layouts) > 0))
	Eventually(func() error {
		return dockerExec(ctx, target.Container, "sh", "-c", grep)
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		Should(Succeed(), "CDI spec on %s never matched gpu.mig.enabled=%t", target.Name, len(layouts) > 0)

	By("setting ClusterPolicy mig.strategy=" + strategy)
	patch := fmt.Sprintf(`{"spec":{"mig":{"strategy":%q}}}`, strategy)
	out, err := h.Kube.KubectlCombined(ctx, "patch", "clusterpolicies.nvidia.com", "cluster-policy", "--type=merge", "-p", patch)
	Expect(err).NotTo(HaveOccurred(), "patch ClusterPolicy mig.strategy=%s: %s", strategy, out)

	for _, ds := range []string{operatorDevicePluginDS, operatorGFDDS} {
		// Waiting for the operator to template the strategy in first, so the
		// restart below is the last rollout rather than one the operator's
		// reconcile then supersedes with pods started under the old layout.
		Eventually(func() (string, error) {
			v, _, err := h.Kube.DaemonSetContainerEnv(ctx, gpuOperatorNamespace, ds, "MIG_STRATEGY")
			return v, err
		}).WithContext(ctx).WithTimeout(config.OperandSettleTimeout()).WithPolling(config.PollInterval()).
			Should(Equal(strategy), "operator never rolled MIG_STRATEGY=%s into %s", strategy, ds)
		rolloutRestart(ctx, h, gpuOperatorNamespace, ds)
	}
}

// procDriverRelPathInSpec identifies the hook in the CDI spec: its argument is
// the staged /proc/driver, which nothing else in the spec references.
const procDriverRelPathInSpec = "driver/proc/driver"

// expectOperandServedMIGCaps asserts the device plugin reads, at the kernel
// path it hardcodes, the capability table the node-agent staged, and that the
// mock's copy is mounted read-only over it. Comparing content rather than
// existence is what ties the plugin's view to this node's layout: a table left
// over from another layout would exist too.
func expectOperandServedMIGCaps(ctx SpecContext, h *harness.Harness, node string) {
	GinkgoHelper()
	plugin := operandPodRef(ctx, h, operatorDevicePluginDS, operatorDevicePluginContainer, node)

	served, err := h.Kube.Exec(ctx, plugin, "cat", operatorProcMigMinors)
	Expect(err).NotTo(HaveOccurred(), "read %s in the device plugin", operatorProcMigMinors)
	staged, err := h.Kube.Exec(ctx, nvmlPodOnNode(ctx, h, node), "cat", migMinorsPath)
	Expect(err).NotTo(HaveOccurred(), "read the staged capability table on %s", node)
	Expect(served.Stdout).NotTo(BeEmpty(), "empty capability table in the device plugin")
	Expect(served.Stdout).To(Equal(staged.Stdout), "device plugin sees a different table than the one staged on %s", node)

	mounts, err := h.Kube.Exec(ctx, plugin, "cat", "/proc/self/mountinfo")
	Expect(err).NotTo(HaveOccurred(), "read the device plugin's mountinfo")
	Expect(mountOptions(mounts.Stdout, "/proc/driver")).To(HavePrefix("ro"),
		"/proc/driver in the device plugin is not a read-only mount")
}

// expectPodGetsOnePartition runs a pod requesting one resource on node and
// asserts it was handed a single parent GPU and the two capability nodes, one
// for the GPU instance and one for the compute instance, that open exactly one
// partition on it.
func expectPodGetsOnePartition(ctx SpecContext, h *harness.Harness, node, name, resource string) {
	GinkgoHelper()
	manifest := pod.Spec{
		Name:        name,
		Namespace:   migWorkloadNS,
		Image:       migWorkloadImage,
		Node:        node,
		GPUs:        1,
		GPUResource: resource,
		Command:     []string{"/bin/sh", "-c"},
		Args:        []string{"trap 'exit 0' TERM; sleep 3600 & wait"},
	}.Render()
	Expect(h.Kube.Apply(ctx, manifest)).To(Succeed(), "apply workload %s", name)
	DeferCleanup(func(ctx SpecContext) { _ = h.Kube.Delete(ctx, manifest) })
	assertions.WaitPodPhase(ctx, h.Kube, migWorkloadNS, name, "Running", config.ReadyTimeout(), config.PollInterval())
	ref := kube.PodRef{Namespace: migWorkloadNS, Pod: name}

	caps, err := h.Kube.Exec(ctx, ref, "ls", migCapDevDir)
	Expect(err).NotTo(HaveOccurred(), "list %s in %s", migCapDevDir, name)
	Expect(strings.Fields(caps.Stdout)).To(HaveLen(2), "cap nodes handed to %s", name)

	gpus, err := h.Kube.ExecSh(ctx, ref, `ls /dev | grep -E '^nvidia[0-9]+$'`)
	Expect(err).NotTo(HaveOccurred(), "list GPU device nodes in %s", name)
	Expect(strings.Fields(gpus.Stdout)).To(HaveLen(1), "parent GPUs handed to %s", name)
}

// migResource is the resource migStrategy=mixed publishes a slice profile as.
func migResource(profile string) string { return "nvidia.com/mig-" + profile }

// migSlices is the GPU-instance slice count a profile name leads with, which
// is what GFD publishes as slices.gi: 3 for "3g.40gb".
func migSlices(profile string) int {
	n, _, _ := strings.Cut(profile, "g.")
	slices, err := strconv.Atoi(n)
	Expect(err).NotTo(HaveOccurred(), "MIG profile %q does not lead with a slice count", profile)
	return slices
}

// mountOptions returns the per-mount options of the mount at target in a
// mountinfo table, "" when nothing is mounted there.
func mountOptions(mountinfo, target string) string {
	for line := range strings.Lines(mountinfo) {
		// Fields: ID, parent ID, major:minor, root, mount point, options, ...
		f := strings.Fields(line)
		if len(f) > 5 && f[4] == target {
			return f[5]
		}
	}
	return ""
}
