//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package e2e

import (
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
)

// Each spec owns its namespace. The four-Node rack is installed once and can
// be reused for a focused rerun without touching workloads in other namespaces.
func setupKWOKClaims(ctx SpecContext) (*harness.Harness, string, []string) {
	GinkgoHelper()
	h := setupCluster(ctx, "kwok-dra")
	ns := fmt.Sprintf("kwok-claims-%d", time.Now().UnixNano())
	out, err := h.Kube.KubectlCombined(ctx, "create", "namespace", ns)
	Expect(err).NotTo(HaveOccurred(), out)
	DeferCleanup(func(ctx SpecContext) {
		out, err := h.Kube.KubectlCombined(ctx, "delete", "namespace", ns, "--timeout=2m")
		Expect(err).NotTo(HaveOccurred(), out)
	})
	var list corev1.NodeList
	Expect(h.Kube.GetJSON(ctx, &list, "nodes", "-l", "mokka.nvidia.com/pool=kwok-dra-e2e")).To(Succeed())
	Expect(list.Items).To(HaveLen(4))
	nodes := make([]string, 0, len(list.Items))
	for _, n := range list.Items {
		Expect(n.Annotations).To(HaveKeyWithValue("kwok.x-k8s.io/node", "fake"))
		nodes = append(nodes, n.Name)
	}
	slices.Sort(nodes)
	return h, ns, nodes
}

func applyKWOKClaimPod(ctx SpecContext, h *harness.Harness, ns, name, template, node string) {
	GinkgoHelper()
	// Pin through a selector so the scheduler still allocates the claim.
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata: {name: %s, namespace: %s}
spec:
  nodeSelector: {kubernetes.io/hostname: %s}
  tolerations: [{key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}]
  containers:
    - name: job
      image: registry.k8s.io/pause:3.10
      resources: {claims: [{name: device}]}
  resourceClaims: [{name: device, resourceClaimTemplateName: %s}]
`, name, ns, node, template)
	Expect(h.Kube.Apply(ctx, []byte(manifest))).To(Succeed())
}

func waitKWOKClaimPod(ctx SpecContext, h *harness.Harness, ns, name, node string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var pod corev1.Pod
		g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", name, "-n", ns)).To(Succeed())
		g.Expect(pod.Spec.NodeName).To(Equal(node))
		g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
}
