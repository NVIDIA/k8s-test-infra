//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

func setupStandaloneProfile(ctx context.Context, h *harness.Harness, name string) (profile.Profile, kube.PodRef, string) {
	GinkgoHelper()
	p := loadProfile(name)
	installDemoChart(ctx, h, name, p.ExpectedGPUs())
	assertions.WaitDaemonSetReady(ctx, h.Kube, nvmlMockNamespace, "nvml-mock", config.ReadyTimeout(), config.PollInterval())
	pod := firstNvmlPod(ctx, h)
	return p, pod, podNode(ctx, h, pod)
}
