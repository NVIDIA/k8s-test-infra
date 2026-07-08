//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/nodes"
)

// NVMLSymlink asserts the versioned libnvidia-ml.so.1 symlink exists on the
// node (shared by every scenario's "verify mock files" step).
func NVMLSymlink(ctx context.Context, n nodes.Docker, node string) {
	ginkgo.GinkgoHelper()
	ginkgo.By("libnvidia-ml.so.1 symlink present on node")
	ok, err := n.TestSymlink(ctx, node, NVMLLink)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ok).To(gomega.BeTrue(), "missing NVML symlink %s", NVMLLink)
}

// nodeFileExists asserts path exists on the node.
func nodeFileExists(ctx context.Context, n nodes.Docker, node, path string) {
	ginkgo.GinkgoHelper()
	ok, err := n.Test(ctx, node, path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ok).To(gomega.BeTrue(), "missing file on node: %s", path)
}

// ConfigContains asserts the node config file at path contains substr (parity
// with the `grep -q "num_devices: N"` checks).
func ConfigContains(ctx context.Context, n nodes.Docker, node, path, substr string) {
	ginkgo.GinkgoHelper()
	ginkgo.By(fmt.Sprintf("%s contains %q", path, substr))
	res, err := n.ExecSh(ctx, node, fmt.Sprintf("grep -F -- %q %s", substr, path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"%q not found in %s:\n%s", substr, path, res.Combined())
}

// DevicePluginMockFiles ports the e2e-device-plugin "Verify mock files on node"
// step: NVML symlink, dev nodes (nvidia0/nvidiactl, and nvidia1 when count>=2),
// config at BOTH locations, and the profile-derived num_devices line.
func DevicePluginMockFiles(ctx context.Context, n nodes.Docker, node string, gpuCount int) {
	ginkgo.GinkgoHelper()
	NVMLSymlink(ctx, n, node)

	ginkgo.By("device node files present")
	nodeFileExists(ctx, n, node, "/var/lib/nvml-mock/driver/dev/nvidia0")
	nodeFileExists(ctx, n, node, "/var/lib/nvml-mock/driver/dev/nvidiactl")
	if gpuCount >= 2 {
		nodeFileExists(ctx, n, node, "/var/lib/nvml-mock/driver/dev/nvidia1")
	}

	ginkgo.By("config present at both locations")
	nodeFileExists(ctx, n, node, "/var/lib/nvml-mock/config/config.yaml")
	nodeFileExists(ctx, n, node, "/var/lib/nvml-mock/driver/config/config.yaml")
	ConfigContains(ctx, n, node, "/var/lib/nvml-mock/config/config.yaml",
		fmt.Sprintf("num_devices: %d", gpuCount))
}

// DRAMockFiles ports the e2e-dra "Verify mock files on node" step: driver-root
// config only, plus the num_devices line and the NVML symlink.
func DRAMockFiles(ctx context.Context, n nodes.Docker, node string, gpuCount int) {
	ginkgo.GinkgoHelper()
	nodeFileExists(ctx, n, node, "/var/lib/nvml-mock/driver/config/config.yaml")
	ConfigContains(ctx, n, node, "/var/lib/nvml-mock/driver/config/config.yaml",
		fmt.Sprintf("num_devices: %d", gpuCount))
	NVMLSymlink(ctx, n, node)
}
