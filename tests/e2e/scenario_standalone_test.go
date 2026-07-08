//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/profile"
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
	It("records node labels (informational)", func(ctx SpecContext) {
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
				p = loadProfile(name)
				installDemoChart(ctx, h, name, p.ExpectedGPUs())
				assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
				pod = firstNvmlPod(ctx, h)
				node = podNode(ctx, h, pod)
			})

			It("renders the fake-GPU-operator profile ConfigMaps", func(ctx SpecContext) {
				assertions.ProfileConfigMaps(ctx, h.Kube, nvmlMockNamespace, fgoProfileSelector, fgoProfileConfigMin)
			})

			It("reports the profile GPUs via nvidia-smi", func(ctx SpecContext) {
				assertions.NvidiaSMI(ctx, h.Nodes, node, p)
				assertions.NvidiaSMIPod(ctx, h.Kube, pod, p)
			})

			It("exposes the NVLink topology (gated on fabricmanager)", func(ctx SpecContext) {
				assertions.FabricManagerGate(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", pod, config.ReadyTimeout(), config.PollInterval())
				assertions.NVLink(ctx, h.Nodes, node, p)
			})

			It("exposes the InfiniBand mock", func(ctx SpecContext) {
				assertions.IBStat(ctx, h.Kube, pod, p)
				if p.IBEnabled() {
					assertions.IBVDevinfo(ctx, h.Kube, pod, p)
					// demo.sh step 8: `ibstatus | head -40` — informational dump.
					if res, err := h.Kube.ExecSh(ctx, pod, "ibstatus | head -40"); err == nil {
						AddReportEntry("ibstatus (in-pod, first 40 lines)", res.Combined())
					}
				}
			})

			It("renders the PCI sysfs topology", func(ctx SpecContext) {
				assertions.PCISysfs(ctx, h.Kube, pod, p.ExpectedGPUs(), p.ExpectedPCIRoots())
			})

			It("performs cross-node ibping + iblinkinfo", func(ctx SpecContext) {
				if !p.IBEnabled() {
					Skip("InfiniBand disabled for profile " + name)
				}
				pods, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, "app.kubernetes.io/name=nvml-mock")
				Expect(err).NotTo(HaveOccurred())
				Expect(len(pods)).To(BeNumerically(">=", 2),
					"need >= 2 running nvml-mock pods for cross-node ibping, found %d", len(pods))
				server := kube.PodRef{Namespace: nvmlMockNamespace, Pod: pods[0]}
				client := kube.PodRef{Namespace: nvmlMockNamespace, Pod: pods[1]}
				assertions.IBPing(ctx, h.Kube, server, client, "both", ibPingRetries, ibPingRetrySleep)
				assertions.IBLinkInfo(ctx, h.Kube, server, client, p)
			})

			It("handles GPU failure injection modes", Label("failure-injection"), func(ctx SpecContext) {
				if name != "h100" {
					Skip("failure-injection demo is defined against the h100 profile")
				}
				runFailureInjectionScenarios(ctx, h, pod, p.ExpectedGPUs())
			})
		})
	}
})

func runFailureInjectionScenarios(ctx SpecContext, h *harness.Harness, pod kube.PodRef, expectedGPUs int) {
	GinkgoHelper()

	By("failure-injection healthy baseline")
	cfg := configMapData(ctx, h)
	Expect(cfg).NotTo(ContainSubstring("failure:"), "healthy baseline should not render a failure block")
	listCount := nvidiaSMILCount(ctx, h, pod)
	Expect(listCount).To(Equal(expectedGPUs), "healthy baseline should list all profile GPUs")

	eccPod := upgradeFailureMode(ctx, h, "ecc_uncorrectable", map[string]string{
		"gpu.failureInjection.enabled":     "true",
		"gpu.failureInjection.mode":        "ecc_uncorrectable",
		"gpu.failureInjection.after_calls": "1",
		"gpu.failureInjection.xid.code":    "79",
	})
	assertConfigContains(ctx, h, "mode: ecc_uncorrectable")
	Expect(nvidiaSMILCount(ctx, h, eccPod)).To(Equal(expectedGPUs), "ecc_uncorrectable keeps devices addressable")
	Expect(maxIntegerLine(eccQuery(ctx, h, eccPod))).To(BeNumerically(">", 0), "ecc_uncorrectable should trip ECC counters")

	lostPod := upgradeFailureMode(ctx, h, "lost", map[string]string{
		"gpu.failureInjection.mode":        "lost",
		"gpu.failureInjection.after_calls": "1",
		"gpu.failureInjection.xid.code":    "0",
	})
	assertConfigContains(ctx, h, "mode: lost")
	Expect(hasFailureMarker(temperatureQuery(ctx, h, lostPod))).To(BeTrue(), "lost mode should surface nvidia-smi error markers")

	fobPod := upgradeFailureMode(ctx, h, "fallen_off_bus", map[string]string{
		"gpu.failureInjection.mode":        "fallen_off_bus",
		"gpu.failureInjection.after_calls": "1",
		"gpu.failureInjection.xid.code":    "79",
	})
	assertConfigContains(ctx, h, "mode: fallen_off_bus")
	assertConfigContains(ctx, h, "code: 79")
	Expect(hasFailureMarker(temperatureQuery(ctx, h, fobPod))).To(BeTrue(), "fallen_off_bus should surface nvidia-smi error markers")
}

func upgradeFailureMode(ctx SpecContext, h *harness.Harness, mode string, set map[string]string) kube.PodRef {
	GinkgoHelper()
	By("helm upgrade --reuse-values failure mode " + mode)
	err := h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:        "nvml-mock",
		Chart:       chartDir(),
		Namespace:   nvmlMockNamespace,
		ReuseValues: true,
		Set:         set,
		Wait:        true,
		Timeout:     config.HelmTimeout(),
	})
	Expect(err).NotTo(HaveOccurred(), "helm upgrade failure mode %s", mode)
	_, _ = h.Kube.KubectlCombined(ctx, "delete", "pods", "-n", nvmlMockNamespace, "-l", "app.kubernetes.io/name=nvml-mock", "--ignore-not-found")
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	return firstNvmlPod(ctx, h)
}

func configMapData(ctx SpecContext, h *harness.Harness) string {
	GinkgoHelper()
	out, err := h.Kube.KubectlCombined(ctx, "get", "configmap/nvml-mock-config", "-n", nvmlMockNamespace, "-o", "jsonpath={.data.config\\.yaml}")
	Expect(err).NotTo(HaveOccurred(), "read nvml-mock configmap")
	return out
}

func assertConfigContains(ctx SpecContext, h *harness.Harness, needle string) {
	GinkgoHelper()
	Expect(configMapData(ctx, h)).To(ContainSubstring(needle), "ConfigMap should contain %q", needle)
}

func nvidiaSMILCount(ctx SpecContext, h *harness.Harness, pod kube.PodRef) int {
	GinkgoHelper()
	res, err := h.Kube.Exec(ctx, pod, "nvidia-smi", "-L")
	Expect(err).NotTo(HaveOccurred(), "nvidia-smi -L: %s", res.Combined())
	count := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "GPU ") {
			count++
		}
	}
	return count
}

func eccQuery(ctx SpecContext, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	res, _ := h.Kube.Exec(ctx, pod, "nvidia-smi", "--query-gpu=ecc.errors.uncorrected.aggregate.total", "--format=csv,noheader,nounits")
	return res.Combined()
}

func temperatureQuery(ctx SpecContext, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	res, _ := h.Kube.Exec(ctx, pod, "nvidia-smi", "--query-gpu=temperature.gpu", "--format=csv,noheader,nounits")
	return res.Combined()
}

func maxIntegerLine(out string) int {
	max := 0
	for _, line := range strings.Split(out, "\n") {
		v, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && v > max {
			max = v
		}
	}
	return max
}

func hasFailureMarker(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "[n/a]") ||
		strings.Contains(lower, "[unknown error]") ||
		strings.Contains(lower, "gpu is lost") ||
		strings.Contains(lower, "err")
}
