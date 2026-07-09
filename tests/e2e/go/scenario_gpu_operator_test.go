//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"os"
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

			It("installs GPU Operator and publishes GPUs", func(ctx SpecContext) {
				installGPUOperator(ctx, h)
				waitOperatorValidatorRunning(ctx, h)
				for _, label := range []string{"nvidia.com/gpu.product", "nvidia.com/gpu.memory", "nvidia.com/gpu.count"} {
					assertions.NodeLabelSoft(ctx, h.Kube, node, label)
				}
				assertions.WaitAllocatableGPU(ctx, h.Kube, node, p.ExpectedGPUs(), config.ReadyTimeout(), config.PollInterval())
			})
		})
	}
})

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
