//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assets"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
)

const (
	devicePluginNamespace = "kube-system"
	devicePluginName      = "nvidia-device-plugin-mock"
	devicePluginSelector  = "name=nvidia-device-plugin-mock"
)

// deployDevicePlugin applies the upstream device plugin DaemonSet and waits for
// node to advertise expectedGPUs. Pods are deleted after the apply because the
// plugin builds its device map once at startup, so one left running by an
// earlier spec would keep serving the GPUs the mock had then.
func deployDevicePlugin(ctx SpecContext, h *harness.Harness, node string, expectedGPUs int) {
	GinkgoHelper()
	Expect(h.Kube.Apply(ctx, assets.DevicePluginManifest)).To(Succeed(), "apply device plugin manifest")
	Expect(h.Kube.DeletePodsByLabel(ctx, devicePluginNamespace, devicePluginSelector)).To(Succeed(), "restart device plugin pods")
	assertions.WaitDaemonSetReady(ctx, h.Kube, devicePluginNamespace, devicePluginName, config.ReadyTimeout(), config.PollInterval())
	assertions.WaitAllocatableGPU(ctx, h.Kube, node, expectedGPUs, config.ReadyTimeout(), config.PollInterval())
}
