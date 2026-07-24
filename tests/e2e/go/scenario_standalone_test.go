//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	ibPingRetries    = 5
	ibPingRetrySleep = 10 * time.Second

	// fgoProfileSelector matches the per-profile ConfigMaps the fake GPU
	// operator integration renders; the chart ships 7, demo asserts >= 6.
	fgoProfileSelector  = "run.ai/gpu-profile=true"
	fgoProfileConfigMin = 6
)

// Go port of docs/demo/standalone/demo.sh. ONE shared multi-node cluster is
// created once (BeforeAll on the outer Ordered container); each selected GPU
// profile then re-installs the chart via `helm upgrade --install` (a chart
// upgrade, not a cluster rebuild) and runs the demo's validation steps against
// that same cluster. Set E2E_PROFILES to scope which profiles run (default:
// gb200); locally `make e2e E2E_PROFILES=a100` is the fast inner loop.
var _ = Describe("nvml-mock standalone", Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, ClusterName, demoKindConfig(selectedProfiles), "standalone")
	})

	// demo.sh step 11: node labels. Cluster topology is static (set by
	// docs/demo/kind.yaml), so capture it once as an informational report entry
	// rather than per profile.
	It("records node labels (informational)", Label("labels"), func(ctx SpecContext) {
		out, err := h.Kube.KubectlCombined(ctx, "get", "nodes", "--show-labels")
		Expect(err).NotTo(HaveOccurred())
		AddReportEntry("node labels", out)
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p    profile.Profile
				pod  kube.PodRef
				node string
			)

			BeforeAll(func(ctx SpecContext) {
				p, pod, node = setupStandaloneProfile(ctx, h, name)
			})

			It("sets the GPU-present node label", Label("labels"), func(ctx SpecContext) {
				assertions.NodeLabelEquals(ctx, h.Kube, node, "nvidia.com/gpu.present", "true")
			})

			It("renders the fake-GPU-operator profile ConfigMaps", Label("fgo"), func(ctx SpecContext) {
				assertions.ProfileConfigMaps(ctx, h.Kube, nvmlMockNamespace, fgoProfileSelector, fgoProfileConfigMin)
			})

			It("lays out the mock driver files on the profile node", Label("mockfiles"), func(ctx SpecContext) {
				assertions.DevicePluginMockFiles(ctx, h.Kube, pod, p.ExpectedGPUs())
			})

			It("reports the profile GPUs via nvidia-smi", Label("nvidia-smi"), func(ctx SpecContext) {
				assertions.NvidiaSMI(ctx, h.Kube, pod, p)
			})

			It("exposes the NVLink topology (gated on fabricmanager)", Label("nvlink"), func(ctx SpecContext) {
				assertions.FabricManagerGate(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", pod, config.ReadyTimeout(), config.PollInterval())
				assertions.NVLink(ctx, h.Kube, pod, p)
			})

			It("exposes the InfiniBand mock", Label("ib"), func(ctx SpecContext) {
				assertions.IBStat(ctx, h.Kube, pod, p)
				if p.IBEnabled() {
					assertions.IBVDevinfo(ctx, h.Kube, pod, p)
					// demo.sh step 8: `ibstatus | head -40` — informational dump.
					if res, err := h.Kube.ExecSh(ctx, pod, "ibstatus | head -40"); err == nil {
						AddReportEntry("ibstatus (in-pod, first 40 lines)", res.Combined())
					}
				}
			})

			It("backs /dev/infiniband with real char devices", Label("ib", "chardev"), func(ctx SpecContext) {
				assertions.IBCharDevices(ctx, h.Kube, pod, p)
			})

			It("advertises the mock NIC to NFD via a local feature file", Label("ib", "nfd"), func(ctx SpecContext) {
				assertions.NFDNicFeatureFile(ctx, h.Kube, pod, p)
			})

			It("renders the PCI sysfs topology", Label("pcisysfs"), func(ctx SpecContext) {
				assertions.PCISysfs(ctx, h.Kube, pod, p.ExpectedGPUs(), p.ExpectedHCAs(), p.ExpectedPCIRoots())
			})

			It("renders the synthesized 15b3 NIC PCI devices", Label("pcisysfs", "nic"), func(ctx SpecContext) {
				assertions.NICSysfs(ctx, h.Kube, pod, p.ExpectedHCAs())
			})

			It("performs cross-node ibping + iblinkinfo", Label("ibping"), func(ctx SpecContext) {
				if !p.IBEnabled() {
					Skip("InfiniBand disabled for profile " + name)
				}
				pods, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, nvmlMockSelector)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(pods)).To(BeNumerically(">=", 2),
					"need >= 2 running nvml-mock pods for cross-node ibping, found %d", len(pods))
				server := kube.PodRef{Namespace: nvmlMockNamespace, Pod: pods[0]}
				client := kube.PodRef{Namespace: nvmlMockNamespace, Pod: pods[1]}
				assertions.IBPing(ctx, h.Kube, server, client, "both", ibPingRetries, ibPingRetrySleep)
				assertions.IBLinkInfo(ctx, h.Kube, server, client, p)
			})

			Context("failure injection", Label("failure-injection"), Ordered, func() {
				It("starts from a healthy baseline", func(ctx SpecContext) {
					assertFailureInjectionBaseline(ctx, h, pod, p.ExpectedGPUs())
				})

				// Runtime control runs before the Helm-driven injections below:
				// the consumer (pod) is still the original, running DaemonSet pod,
				// so we validate the "both observers, no restart" path against it.
				// Each scenario maps to a documented nvml-mock-ctl example
				// (docs/nvml-mock-ctl.md) and validates the effect via nvidia-smi.
				It("injects ECC at runtime via nvml-mock-ctl without restart", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeECCInjection(ctx, h, pod)
				})

				It("marks all GPUs lost and recovers via nvml-mock-ctl", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeFailAllLost(ctx, h, pod, p.ExpectedGPUs())
				})

				It("sets a per-GPU field via nvml-mock-ctl set", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeSetField(ctx, h, pod)
				})

				It("pins a per-GPU temperature via nvml-mock-ctl set", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeSetTemperature(ctx, h, pod)
				})

				It("pins temperature via the nvml-mock-ctl temp command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeTempCommand(ctx, h, pod)
				})

				It("pins power draw via the nvml-mock-ctl power command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimePowerCommand(ctx, h, pod)
				})

				It("pins fan speed via the nvml-mock-ctl fan command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeFanCommand(ctx, h, pod)
				})

				It("pins utilization via the nvml-mock-ctl util command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeUtilCommand(ctx, h, pod)
				})

				It("pins SM/graphics clocks via the nvml-mock-ctl clocks command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeClocksCommand(ctx, h, pod)
				})

				It("sets a throttle reason via the nvml-mock-ctl throttle command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeThrottleCommand(ctx, h, pod)
				})

				It("pins the performance state via the nvml-mock-ctl pstate command", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimePStateCommand(ctx, h, pod)
				})

				It("targets a GPU by UUID via nvml-mock-ctl", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeUUIDTargeting(ctx, h, pod)
				})

				It("reports active overrides via nvml-mock-ctl status", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeStatus(ctx, h, pod)
				})

				It("recovers a GPU via nvml-mock-ctl fail --mode healthy", Label("runtime-control"), func(ctx SpecContext) {
					assertRuntimeHealthyRecovery(ctx, h, pod)
				})

				It("injects rising NVLink DL errors via the nvml-mock-ctl nvlink-error command", Label("runtime-control"), func(ctx SpecContext) {
					if p.ExpectedNV() == 0 {
						Skip("profile " + name + " has no NVLink switch topology; nvlink-error has no links to fault")
					}
					// nvlink -e is read through fabricmanager; gate on it being
					// ready before injecting, matching the topology assertion.
					assertions.FabricManagerGate(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", pod, config.ReadyTimeout(), config.PollInterval())
					assertRuntimeNVLinkErrorInjection(ctx, h, pod)
				})

				It("injects ECC uncorrectable errors", func(ctx SpecContext) {
					assertECCUncorrectableFailure(ctx, h, p.ExpectedGPUs())
				})

				It("injects lost GPU errors", func(ctx SpecContext) {
					assertLostGPUFailure(ctx, h)
				})

				It("injects fallen-off-bus errors", func(ctx SpecContext) {
					assertFallenOffBusFailure(ctx, h)
				})
			})
		})
	}
})
