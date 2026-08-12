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

// nvmlPodOnNode returns the running nvml-mock DaemonSet pod scheduled on the
// given node. Runtime overrides on the mock are per-node (hostPath-staged), so
// a caller that reads back the effect through a per-node consumer (dcgm-exporter,
// nvidia-smi in the same pod) must pin both to the same node; firstNvmlPod's
// name-sort tiebreak is not enough when more than one mock pod is running.
func nvmlPodOnNode(ctx context.Context, h *harness.Harness, node string) kube.PodRef {
	GinkgoHelper()
	var name string
	Eventually(func() (string, error) {
		names, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, nvmlMockSelector)
		if err != nil {
			return "", err
		}
		for _, n := range names {
			on, nodeErr := h.Kube.PodNode(ctx, nvmlMockNamespace, n)
			if nodeErr != nil {
				return "", nodeErr
			}
			if on == node {
				name = n
				return name, nil
			}
		}
		return "", nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).
		ShouldNot(BeEmpty(), "no running nvml-mock pod on node %s", node)
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

// setupCluster wires adapters to the externally-owned cluster identified by
// E2E_CLUSTER_NAME / E2E_KUBE_CONTEXT (Tilt provisions the cluster and rolls
// out the mock; the suite only observes). Failure diagnostics under
// artifacts/<diagSub...> are collected on any spec failure.
func setupCluster(ctx context.Context, diagSub ...string) *harness.Harness {
	GinkgoHelper()
	h, err := harness.New(ctx, config.ClusterName(), config.KubeContext(), builtImage)
	DeferCleanup(func(ctx SpecContext) { //nolint:contextcheck // Ginkgo cleanup ctx is intentionally distinct from the outer spec ctx
		collectOnFailure(ctx, h, diagSub...)
	})
	Expect(err).NotTo(HaveOccurred(), "attach cluster name=%q context=%q", config.ClusterName(), config.KubeContext())
	return h
}
