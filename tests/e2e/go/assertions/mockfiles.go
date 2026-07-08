//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// NVMLSymlink asserts the versioned libnvidia-ml.so.1 symlink exists on the
// nvml-mock pod (shared by every scenario's "verify mock files" step).
func NVMLSymlink(ctx context.Context, k *kube.Client, pod kube.PodRef) {
	ginkgo.GinkgoHelper()
	ginkgo.By("libnvidia-ml.so.1 symlink present")
	res, err := k.Exec(ctx, pod, "test", "-L", NVMLLink)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "missing NVML symlink %s:\n%s", NVMLLink, res.Combined())
}

// podFileExists asserts path exists from inside the nvml-mock pod.
func podFileExists(ctx context.Context, k *kube.Client, pod kube.PodRef, path string) {
	ginkgo.GinkgoHelper()
	res, err := k.Exec(ctx, pod, "test", "-e", path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "missing file: %s\n%s", path, res.Combined())
}

// ConfigContains asserts the config file at path contains substr (parity
// with the `grep -q "num_devices: N"` checks).
func ConfigContains(ctx context.Context, k *kube.Client, pod kube.PodRef, path, substr string) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("%s contains %q", path, substr))
	res, err := k.ExecSh(ctx, pod, fmt.Sprintf("grep -F -- %q %s", substr, path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"%q not found in %s:\n%s", substr, path, res.Combined())
}

// DevicePluginMockFiles ports the e2e-device-plugin "Verify mock files"
// step: NVML symlink, dev nodes (nvidia0/nvidiactl, and nvidia1 when count>=2),
// config at BOTH locations, and the profile-derived num_devices line.
func DevicePluginMockFiles(ctx context.Context, k *kube.Client, pod kube.PodRef, gpuCount int) {
	ginkgo.GinkgoHelper()
	NVMLSymlink(ctx, k, pod)

	ginkgo.By("device node files present")
	podFileExists(ctx, k, pod, "/var/lib/nvml-mock/driver/dev/nvidia0")
	podFileExists(ctx, k, pod, "/var/lib/nvml-mock/driver/dev/nvidiactl")
	if gpuCount >= 2 {
		podFileExists(ctx, k, pod, "/var/lib/nvml-mock/driver/dev/nvidia1")
	}

	ginkgo.By("config present at both locations")
	podFileExists(ctx, k, pod, "/var/lib/nvml-mock/config/config.yaml")
	podFileExists(ctx, k, pod, "/var/lib/nvml-mock/driver/config/config.yaml")
	ConfigContains(ctx, k, pod, "/var/lib/nvml-mock/config/config.yaml",
		fmt.Sprintf("num_devices: %d", gpuCount))
}

// DRAMockFiles ports the e2e-dra "Verify mock files on node" step: driver-root
// config only, plus the num_devices line and the NVML symlink.
func DRAMockFiles(ctx context.Context, k *kube.Client, pod kube.PodRef, gpuCount int) {
	ginkgo.GinkgoHelper()
	podFileExists(ctx, k, pod, "/var/lib/nvml-mock/driver/config/config.yaml")
	ConfigContains(ctx, k, pod, "/var/lib/nvml-mock/driver/config/config.yaml",
		fmt.Sprintf("num_devices: %d", gpuCount))
	NVMLSymlink(ctx, k, pod)
}
