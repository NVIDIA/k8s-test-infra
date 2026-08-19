//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const gpuOperatorNamespace = "gpu-operator"

var _ = Describe("nvml-mock GPU Operator", Label("gpu-operator"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, "gpu-operator")
	})

	for _, name := range selectedProfiles {
		name := name
		Context("profile "+name, Label(name), Ordered, func() {
			var (
				p    profile.Profile
				node string
			)

			BeforeAll(func(ctx SpecContext) {
				var podName string
				p, _, _ = setupStandaloneProfile(ctx, h, name)
				// Not the node setupStandaloneProfile returns: that comes from
				// FirstPodName over the nvml-mock DaemonSet, which can land on
				// the CP (mock tolerates `operator: Exists`), and the Operator's
				// operands don't tolerate the CP NoSchedule taint — WaitGFDLabels
				// and WaitAllocatableGPU would time out on a CP-derived node.
				node = gpuOperatorTargetNode(ctx, h)
				cp, err := h.Cluster.ControlPlane(ctx)
				Expect(err).NotTo(HaveOccurred())
				podName = cp.Name
				verifyGPUOperatorNodeSetup(ctx, podName)

				// Wait belongs here, not in a spec: every spec below reads state
				// that only the operator publishes, and `helm --wait` covers only
				// the operator's own release. The specs assert on the ClusterPolicy
				// operands (device plugin, GFD, dcgm-exporter), which the operator
				// creates afterwards — a narrow label filter would race the operand
				// rollout without this barrier (#561).
				waitOperatorValidatorRunning(ctx, h)
			})

			It("publishes GFD labels and allocatable GPUs", Label("device-plugin"), func(ctx SpecContext) {
				// Hard assertion, derived from the profile rather than read back
				// off the node. The previous warning-only check could never fail,
				// so GPU Feature Discovery could have been removed entirely and
				// this scenario would still have gone green.
				assertions.WaitGFDLabels(ctx, h.Kube, node,
					assertions.ExpectedGFDLabels(p.GFDProductName(), p.MemoryMiB(), p.ExpectedGPUs()),
					config.ReadyTimeout(), config.PollInterval())
				assertions.WaitAllocatableGPU(ctx, h.Kube, node, p.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
			})

			It("serves the rendered PCI tree to the GFD container at the kernel paths", Label("device-plugin"), func(ctx SpecContext) {
				// The NVML-derived labels above cannot distinguish "GFD read
				// the mock tree" from "GFD read the host's sysfs and happened
				// to agree". Reading the tree from inside the container pins
				// the delivery itself, independent of what GFD makes of it.
				pod, err := h.Kube.FirstPodName(ctx, gpuOperatorNamespace, "app=gpu-feature-discovery")
				Expect(err).NotTo(HaveOccurred())
				assertions.PCISysfsAtKernelPath(ctx, h.Kube,
					kube.PodRef{Namespace: gpuOperatorNamespace, Pod: pod, Container: "gpu-feature-discovery"},
					p.ExpectedGPUs())
			})

			It("exports DCGM device metrics that vary over time", Label("dcgm"), func(ctx SpecContext) {
				assertions.DCGMDeviceMetrics(ctx, h.Kube, gpuOperatorNamespace,
					p.DisplayName, p.ExpectedGPUs(), gpmProfiles[name],
					config.ReadyTimeout(), config.PollInterval())
			})

			It("pins a single-GPU temperature surfaced through dcgm-exporter without restart", Label("dcgm", "runtime-control"), func(ctx SpecContext) {
				assertRuntimeTempViaDCGM(ctx, h, tempPinC)
			})

			It("pins a single-GPU power draw surfaced through dcgm-exporter without restart", Label("dcgm", "runtime-control"), func(ctx SpecContext) {
				assertRuntimePowerViaDCGM(ctx, h)
			})

			It("surfaces a runtime single-GPU failure through dcgm-exporter without restart", Label("dcgm", "runtime-control"), func(ctx SpecContext) {
				assertRuntimeXidViaDCGM(ctx, h, xidTestCode)
			})

			It("surfaces an injected Xid through dcgm-exporter", Label("dcgm", "xid"), func(ctx SpecContext) {
				// Runs last: leaves the mock in a failed state.
				injectXidAndValidate(ctx, h, xidTestCode)
			})
		})
	}
})

// gpmProfiles are the Hopper+ profiles that serve DCGM_FI_PROF_* GPM metrics.
var gpmProfiles = map[string]bool{"h100": true, "b200": true, "gb200": true, "gb300": true}

// xidTestCode is the Xid injected and asserted on (79 = GPU fallen off the bus).
const xidTestCode = 79

// tempPinC is the temperature pinned at runtime and asserted through
// dcgm-exporter. Distinct from the dynamic-metrics baseline and below every
// profile's shutdown threshold, so DCGM never clamps the reading and the change
// is unambiguous.
const tempPinC = 85

// assertRuntimeTempViaDCGM pins a single GPU's temperature at runtime via
// nvml-mock-ctl — no Helm upgrade, no pod restart — and asserts the already-
// running dcgm-exporter reports the pinned DCGM_FI_DEV_GPU_TEMP for that GPU
// only, picking it up through the bind-mounted runtime config override within the TTL.
// The mock's override file is per-node and dcgm-exporter runs per-node, so we
// pin both the nvml-mock-ctl target and the scraped exporter to the same node.
func assertRuntimeTempViaDCGM(ctx SpecContext, h *harness.Harness, wantC int) {
	GinkgoHelper()
	const targetGPU = 0
	node := gpuOperatorTargetNode(ctx, h)

	By(fmt.Sprintf("pin temperature to %dC on GPU %d at runtime via nvml-mock-ctl on %s (no restart)", wantC, targetGPU, node))
	nvmlMockCtlOnNode(ctx, h, node, "temp", "--gpu", strconv.Itoa(targetGPU), strconv.Itoa(wantC))
	DeferCleanup(func(ctx SpecContext) { nvmlMockCtlOnNode(ctx, h, node, "reset", "--gpu", "all") })

	assertions.DCGMTempReportedForGPU(ctx, h.Kube, gpuOperatorNamespace, node, targetGPU, wantC,
		config.ReadyTimeout(), config.PollInterval())
}

// assertRuntimePowerViaDCGM pins a single GPU's power draw at runtime via
// nvml-mock-ctl — no Helm upgrade, no pod restart — and asserts the already-
// running dcgm-exporter reports the pinned DCGM_FI_DEV_POWER_USAGE (watts) for
// that GPU only. The target watts is chosen inside the profile's advertised
// [min_limit, max_limit] envelope (read from nvidia-smi -q -x so the test is
// profile-agnostic) and far from the dynamic baseline, so the engine never
// clamps it and the change is unambiguous.
func assertRuntimePowerViaDCGM(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	const targetGPU = 0
	node := gpuOperatorTargetNode(ctx, h)

	pod := nvmlPodOnNode(ctx, h, node)
	envelope := smiGPU(ctx, h, pod, targetGPU)
	minF, minOK := envelope.PowerMinLimitW()
	maxF, maxOK := envelope.PowerMaxLimitW()
	Expect(minOK && maxOK).To(BeTrue(), "profile must report a numeric power envelope")
	minW, maxW := int(minF), int(maxF)
	Expect(maxW).To(BeNumerically(">", minW), "profile must advertise a usable power envelope")
	baseline := smiGPUPowerDrawW(ctx, h, pod, targetGPU)

	lo := minW + (maxW-minW)/4
	hi := minW + (maxW-minW)*3/4
	wantW := lo
	if absInt(hi-baseline) > absInt(lo-baseline) {
		wantW = hi
	}

	By(fmt.Sprintf("pin power draw to %dW on GPU %d at runtime via nvml-mock-ctl on %s (no restart)", wantW, targetGPU, node))
	nvmlMockCtlOnNode(ctx, h, node, "power", "--gpu", strconv.Itoa(targetGPU), strconv.Itoa(wantW))
	DeferCleanup(func(ctx SpecContext) { nvmlMockCtlOnNode(ctx, h, node, "reset", "--gpu", "all") })

	assertions.DCGMPowerReportedForGPU(ctx, h.Kube, gpuOperatorNamespace, node, targetGPU, wantW,
		config.ReadyTimeout(), config.PollInterval())
}

// assertRuntimeXidViaDCGM injects an ecc_uncorrectable failure with a Xid on a
// single GPU at runtime via nvml-mock-ctl — no Helm upgrade, no pod restart —
// and asserts the already-running dcgm-exporter reports the Xid for that GPU
// only, picking it up through the bind-mounted runtime config override within the TTL.
func assertRuntimeXidViaDCGM(ctx SpecContext, h *harness.Harness, xid int) {
	GinkgoHelper()
	const targetGPU = 0
	node := gpuOperatorTargetNode(ctx, h)

	By(fmt.Sprintf("inject ecc_uncorrectable + Xid on GPU 0 at runtime via nvml-mock-ctl on %s (no restart)", node))
	nvmlMockCtlOnNode(ctx, h, node, "fail", "--gpu", strconv.Itoa(targetGPU),
		"--mode", "ecc_uncorrectable", "--after-calls", "1", "--xid", strconv.Itoa(xid))
	DeferCleanup(func(ctx SpecContext) { nvmlMockCtlOnNode(ctx, h, node, "reset", "--gpu", "all") })

	assertions.DCGMXidReportedForGPU(ctx, h.Kube, gpuOperatorNamespace, node, targetGPU, xid,
		config.ReadyTimeout(), config.PollInterval())
}

// gpuOperatorTargetNode picks a node that has both an nvml-mock DaemonSet pod
// (so nvml-mock-ctl and mock-backed operand data are available) and the GPU
// Operator's operands (GFD, device plugin, dcgm-exporter). The operands don't
// tolerate the CP NoSchedule taint, so any worker qualifies whenever workers
// exist; on control-plane-only clusters the CP is the only place both
// DaemonSets can land, so fall back to it.
func gpuOperatorTargetNode(ctx SpecContext, h *harness.Harness) string {
	GinkgoHelper()
	workers, err := h.Cluster.Workers(ctx)
	Expect(err).NotTo(HaveOccurred())
	if len(workers) > 0 {
		return workers[0].Name
	}
	cp, err := h.Cluster.ControlPlane(ctx)
	Expect(err).NotTo(HaveOccurred())
	return cp.Name
}

// injectXidAndValidate enables failure injection, restarts dcgm-exporter so
// DCGM re-initialises against the new mock config, then asserts
// DCGM_FI_DEV_XID_ERRORS. ecc_uncorrectable keeps the device scrapable while
// the Xid event fires.
func injectXidAndValidate(ctx context.Context, h *harness.Harness, xid int) {
	GinkgoHelper()
	By("enabling failure injection (ecc_uncorrectable, xid) on nvml-mock")
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:        "nvml-mock",
		Chart:       chartDir(),
		Namespace:   nvmlMockNamespace,
		ReuseValues: true,
		Set: map[string]string{
			"gpu.failureInjection.enabled":     "true",
			"gpu.failureInjection.mode":        "ecc_uncorrectable",
			"gpu.failureInjection.after_calls": "1",
			"gpu.failureInjection.seed":        "1",
			"gpu.failureInjection.xid.code":    strconv.Itoa(xid),
		},
		Wait:    true,
		Timeout: config.HelmTimeout(),
	})).To(Succeed(), "enable failure injection on nvml-mock")

	// nvml-mock needs no explicit restart: the chart checksums the rendered GPU
	// config into its pod template, so the --wait upgrade above already rolled it.
	rolloutRestart(ctx, h, gpuOperatorNamespace, "nvidia-dcgm-exporter")

	assertions.DCGMXidReported(ctx, h.Kube, gpuOperatorNamespace, xid,
		config.OperandSettleTimeout(), config.PollInterval())
}

// rolloutRestart restarts a DaemonSet and blocks until the rollout completes.
func rolloutRestart(ctx context.Context, h *harness.Harness, ns, ds string) {
	GinkgoHelper()
	_, err := h.Kube.KubectlCombined(ctx, "rollout", "restart", "daemonset/"+ds, "-n", ns)
	Expect(err).NotTo(HaveOccurred(), "rollout restart %s/%s", ns, ds)
	_, err = h.Kube.KubectlCombined(ctx, "rollout", "status", "daemonset/"+ds, "-n", ns, "--timeout=120s")
	Expect(err).NotTo(HaveOccurred(), "rollout status %s/%s", ns, ds)
}

func verifyGPUOperatorNodeSetup(ctx context.Context, node string) {
	GinkgoHelper()
	Expect(dockerExec(ctx, node, "test", "-f", "/var/run/cdi/nvidia.yaml")).To(Succeed(), "CDI spec exists")
	Expect(dockerExec(ctx, node, "bash", "-c", "LD_LIBRARY_PATH=/run/nvidia/driver/usr/lib64 /run/nvidia/driver/usr/bin/nvidia-smi")).To(Succeed(), "nvidia-smi works via /run/nvidia/driver")
}

func waitOperatorValidatorRunning(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	var pod string
	Eventually(func() (string, error) {
		p, err := h.Kube.FirstPodName(ctx, gpuOperatorNamespace, "app=nvidia-operator-validator")
		pod = p
		return p, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "operator validator pod not found")
	assertions.WaitPodPhase(ctx, h.Kube, gpuOperatorNamespace, pod, "Running", 5*time.Minute, config.PollInterval())
}

func dockerExec(ctx context.Context, node string, args ...string) error {
	GinkgoHelper()
	all := append([]string{"exec", node}, args...)
	_, err := runner.Run(ctx, "docker", all...)
	return err
}
