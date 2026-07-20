//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

const (
	gpuOperatorClusterName = "nvml-mock-op"
	gpuOperatorNamespace   = "gpu-operator"
	gpuOperatorRelease     = "gpu-operator"
	gpuOperatorChart       = "nvidia/gpu-operator"
)

var _ = Describe("nvml-mock GPU Operator", Label("gpu-operator"), Ordered, func() {
	var h *harness.Harness
	selectedProfiles := config.SelectedProfileNames()

	BeforeAll(func(ctx SpecContext) {
		h = setupCluster(ctx, gpuOperatorClusterName, assets.KindGPUOperatorConfig, "gpu-operator")
		node, err := h.Cluster.ControlPlane(ctx)
		Expect(err).NotTo(HaveOccurred())
		installNVIDIAContainerToolkit(ctx, h, node)
		configureNVIDIARuntimeCDI(ctx, h, node)
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
				p, _, node = setupStandaloneProfile(ctx, h, name)
				cp, err := h.Cluster.ControlPlane(ctx)
				Expect(err).NotTo(HaveOccurred())
				podName = cp.Name
				verifyGPUOperatorNodeSetup(ctx, podName)
			})

			It("installs GPU Operator and publishes GPUs", Label("device-plugin"), func(ctx SpecContext) {
				installGPUOperator(ctx, h)
				waitOperatorValidatorRunning(ctx, h)
				for _, label := range []string{"nvidia.com/gpu.product", "nvidia.com/gpu.memory", "nvidia.com/gpu.count"} {
					assertions.NodeLabelSoft(ctx, h.Kube, node, label)
				}
				assertions.WaitAllocatableGPU(ctx, h.Kube, node, p.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
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
func assertRuntimeTempViaDCGM(ctx SpecContext, h *harness.Harness, wantC int) {
	GinkgoHelper()
	const targetGPU = 0

	By(fmt.Sprintf("pin temperature to %dC on GPU %d at runtime via nvml-mock-ctl (no restart)", wantC, targetGPU))
	nvmlMockCtl(ctx, h, "temp", "--gpu", strconv.Itoa(targetGPU), strconv.Itoa(wantC))
	DeferCleanup(func(ctx SpecContext) { resetRuntimeOverrides(ctx, h) })

	assertions.DCGMTempReportedForGPU(ctx, h.Kube, gpuOperatorNamespace, targetGPU, wantC,
		config.ReadyTimeout(), config.PollInterval())
}

// assertRuntimePowerViaDCGM pins a single GPU's power draw at runtime via
// nvml-mock-ctl — no Helm upgrade, no pod restart — and asserts the already-
// running dcgm-exporter reports the pinned DCGM_FI_DEV_POWER_USAGE (watts) for
// that GPU only. The target watts is chosen inside the profile's advertised
// [min_limit, max_limit] envelope (queried via nvidia-smi so the test is
// profile-agnostic) and far from the dynamic baseline, so the engine never
// clamps it and the change is unambiguous.
func assertRuntimePowerViaDCGM(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	const targetGPU = 0

	pod := firstNvmlPod(ctx, h)
	minW := int(smiGPUFloat(ctx, h, pod, targetGPU, "power.min_limit"))
	maxW := int(smiGPUFloat(ctx, h, pod, targetGPU, "power.max_limit"))
	Expect(maxW).To(BeNumerically(">", minW), "profile must advertise a usable power envelope")
	baseline := int(smiGPUFloat(ctx, h, pod, targetGPU, "power.draw"))

	lo := minW + (maxW-minW)/4
	hi := minW + (maxW-minW)*3/4
	wantW := lo
	if absInt(hi-baseline) > absInt(lo-baseline) {
		wantW = hi
	}

	By(fmt.Sprintf("pin power draw to %dW on GPU %d at runtime via nvml-mock-ctl (no restart)", wantW, targetGPU))
	nvmlMockCtl(ctx, h, "power", "--gpu", strconv.Itoa(targetGPU), strconv.Itoa(wantW))
	DeferCleanup(func(ctx SpecContext) { resetRuntimeOverrides(ctx, h) })

	assertions.DCGMPowerReportedForGPU(ctx, h.Kube, gpuOperatorNamespace, targetGPU, wantW,
		config.ReadyTimeout(), config.PollInterval())
}

// assertRuntimeXidViaDCGM injects an ecc_uncorrectable failure with a Xid on a
// single GPU at runtime via nvml-mock-ctl — no Helm upgrade, no pod restart —
// and asserts the already-running dcgm-exporter reports the Xid for that GPU
// only, picking it up through the bind-mounted runtime config override within the TTL.
func assertRuntimeXidViaDCGM(ctx SpecContext, h *harness.Harness, xid int) {
	GinkgoHelper()
	const targetGPU = 0

	By("inject ecc_uncorrectable + Xid on GPU 0 at runtime via nvml-mock-ctl (no restart)")
	nvmlMockCtl(ctx, h, "fail", "--gpu", strconv.Itoa(targetGPU),
		"--mode", "ecc_uncorrectable", "--after-calls", "1", "--xid", strconv.Itoa(xid))
	DeferCleanup(func(ctx SpecContext) { resetRuntimeOverrides(ctx, h) })

	assertions.DCGMXidReportedForGPU(ctx, h.Kube, gpuOperatorNamespace, targetGPU, xid,
		config.ReadyTimeout(), config.PollInterval())
}

// injectXidAndValidate enables failure injection, rolls nvml-mock and
// dcgm-exporter to reload the mock config, then asserts DCGM_FI_DEV_XID_ERRORS.
// ecc_uncorrectable keeps the device scrapable while the Xid event fires.
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

	rolloutRestart(ctx, h, nvmlMockNamespace, "nvml-mock")
	rolloutRestart(ctx, h, gpuOperatorNamespace, "nvidia-dcgm-exporter")

	assertions.DCGMXidReported(ctx, h.Kube, gpuOperatorNamespace, xid,
		config.ReadyTimeout(), config.PollInterval())
}

// rolloutRestart restarts a DaemonSet and blocks until the rollout completes.
func rolloutRestart(ctx context.Context, h *harness.Harness, ns, ds string) {
	GinkgoHelper()
	_, err := h.Kube.KubectlCombined(ctx, "rollout", "restart", "daemonset/"+ds, "-n", ns)
	Expect(err).NotTo(HaveOccurred(), "rollout restart %s/%s", ns, ds)
	_, err = h.Kube.KubectlCombined(ctx, "rollout", "status", "daemonset/"+ds, "-n", ns, "--timeout=120s")
	Expect(err).NotTo(HaveOccurred(), "rollout status %s/%s", ns, ds)
}

func installNVIDIAContainerToolkit(ctx context.Context, h *harness.Harness, node cluster.Node) {
	GinkgoHelper()
	Expect(dockerExec(ctx, node.Name, "bash", "-c", `
apt-get update -qq
apt-get install -y -qq curl gpg
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" \
  | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
apt-get update -qq
apt-get install -y -qq nvidia-container-toolkit
`)).To(Succeed(), "install nvidia-container-toolkit in %s", node.Name)
}

func configureNVIDIARuntimeCDI(ctx context.Context, h *harness.Harness, node cluster.Node) {
	GinkgoHelper()
	Expect(dockerExec(ctx, node.Name, "nvidia-ctk", "runtime", "configure", "--runtime=containerd", "--cdi.enabled", "--set-as-default")).
		To(Succeed(), "configure nvidia-container-runtime in %s", node.Name)
	Expect(dockerExec(ctx, node.Name, "bash", "-c", `
cat > /etc/nvidia-container-runtime/config.toml <<'EOF'
[nvidia-container-runtime]
mode = "cdi"

[nvidia-container-runtime.modes.cdi]
default-kind = "nvidia.com/gpu"
spec-dirs = ["/var/run/cdi", "/etc/cdi"]
EOF
systemctl restart containerd
`)).To(Succeed(), "restart containerd in %s", node.Name)
	assertions.WaitNodeReady(ctx, h.Kube, node.Name, config.ReadyTimeout(), config.PollInterval())
}

func verifyGPUOperatorNodeSetup(ctx context.Context, node string) {
	GinkgoHelper()
	Expect(dockerExec(ctx, node, "test", "-f", "/var/run/cdi/nvidia.yaml")).To(Succeed(), "CDI spec exists")
	Expect(dockerExec(ctx, node, "bash", "-c", "LD_LIBRARY_PATH=/run/nvidia/driver/usr/lib64 /run/nvidia/driver/usr/bin/nvidia-smi")).To(Succeed(), "nvidia-smi works via /run/nvidia/driver")
}

func installGPUOperator(ctx SpecContext, h *harness.Harness) {
	GinkgoHelper()
	Expect(h.Helm.RepoAdd(ctx, "nvidia", "https://helm.ngc.nvidia.com/nvidia")).To(Succeed(), "add NVIDIA Helm repo")
	Expect(h.Helm.RepoUpdate(ctx)).To(Succeed(), "update Helm repos")
	valuesFile, err := assets.WriteTemp("gpu-operator-values-*.yaml", assets.GPUOperatorValues)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.Remove(valuesFile) })
	Expect(h.Helm.UpgradeInstall(ctx, helm.Release{
		Name:            gpuOperatorRelease,
		Chart:           gpuOperatorChart,
		Namespace:       gpuOperatorNamespace,
		CreateNamespace: true,
		ValuesFiles:     []string{valuesFile},
		Wait:            true,
		Timeout:         5 * time.Minute,
	})).To(Succeed(), "install GPU Operator")
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
