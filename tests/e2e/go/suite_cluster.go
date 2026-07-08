//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/diagnostics"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// firstNvmlPod returns the first nvml-mock pod in the dedicated e2e namespace.
func firstNvmlPod(ctx context.Context, h *harness.Harness) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		n, err := h.Kube.FirstPodName(ctx, nvmlMockNamespace, nvmlMockSelector)
		name = n
		return n, err
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no nvml-mock pod found")
	return kube.PodRef{Namespace: nvmlMockNamespace, Pod: name}
}

// podNode resolves the Kubernetes node a pod is scheduled on.
func podNode(ctx context.Context, h *harness.Harness, pod kube.PodRef) string {
	GinkgoHelper()
	n, err := h.Kube.PodNode(ctx, pod.Namespace, pod.Pod)
	Expect(err).NotTo(HaveOccurred())
	Expect(n).NotTo(BeEmpty(), "pod %s has no nodeName", pod.Pod)
	return n
}

// collectOnFailure writes diagnostics under artifacts/<sub...> when the current
// spec failed (mirrors the demo/bash "collect logs on failure" blocks).
func collectOnFailure(ctx context.Context, h *harness.Harness, sub ...string) {
	if !CurrentSpecReport().Failed() || h == nil || h.Kube == nil {
		return
	}
	c := diagnostics.New(config.ArtifactsDir(), h.Kube, h.Cluster, sub...)
	c.NvmlMockNamespace = nvmlMockNamespace
	c.Common(ctx)
}

// setupCluster creates the shared cluster (delete-if-exists), wires adapters,
// kind-loads the image, and registers teardown + diagnostics cleanup.
func setupCluster(ctx context.Context, name string, kindConfig []byte, diagSub ...string) *harness.Harness {
	GinkgoHelper()
	h, err := harness.Setup(ctx, name, kindConfig, builtImage)
	DeferCleanup(func(ctx SpecContext) {
		collectOnFailure(ctx, h, diagSub...)
		_ = h.Teardown(ctx, config.KeepCluster())
	})
	Expect(err).NotTo(HaveOccurred(), "setup cluster %q", name)
	return h
}
