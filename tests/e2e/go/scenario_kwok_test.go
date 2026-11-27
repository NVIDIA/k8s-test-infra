//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
)

var _ = Describe("KWOK rack binding", Label("kwok"), func() {
	It("binds each node to its own rack type", func(ctx SpecContext) {
		h := setupCluster(ctx, "kwok")
		realName := config.ClusterName() + "-worker"
		const virtualName = "kwok-worker"
		var cpuNode, kwokNode corev1.Node
		Expect(h.Kube.GetJSON(ctx, &cpuNode, "node", realName)).To(Succeed())
		Expect(h.Kube.GetJSON(ctx, &kwokNode, "node", virtualName)).To(Succeed())

		By("checking both rack slots against the exact node instances")
		Eventually(func(g Gomega) {
			var racks mokkav1.SGPURackList
			g.Expect(h.Kube.GetJSON(ctx, &racks, "sgpuracks", "-l", "mokka.nvidia.com/inventory=binding-e2e")).To(Succeed())
			g.Expect(racks.Items).To(HaveLen(2))
			bound := map[mokkav1.NodeType]string{}
			for _, rack := range racks.Items {
				g.Expect(rack.Spec.Nodes).To(HaveLen(1))
				slot := rack.Spec.Nodes[0]
				expected := cpuNode
				if rack.Spec.NodeType == mokkav1.NodeTypeKWOK {
					expected = kwokNode
				}
				g.Expect(slot.BoundTo(expected.Name, expected.UID)).To(BeTrue(), "rack %s has nodeRef %+v", rack.Name, slot.NodeRef)
				bound[rack.Spec.NodeType] = rack.Spec.Identity.RackGroup
			}
			g.Expect(bound).To(Equal(map[mokkav1.NodeType]string{mokkav1.NodeTypeCPU: "real", mokkav1.NodeTypeKWOK: "virtual"}))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())

		By("checking actual node-daemon Pods after the DaemonSet has converged")
		Eventually(func(g Gomega) {
			var ds appsv1.DaemonSet
			g.Expect(h.Kube.GetJSON(ctx, &ds, "daemonset", "nvml-mock", "-n", nvmlMockNamespace)).To(Succeed())
			g.Expect(ds.Status.ObservedGeneration).To(Equal(ds.Generation))
			g.Expect(ds.Status.DesiredNumberScheduled).To(Equal(int32(1)))
			g.Expect(ds.Status.NumberReady).To(Equal(int32(1)))
			var pods corev1.PodList
			g.Expect(h.Kube.GetJSON(ctx, &pods, "pods", "-n", nvmlMockNamespace, "-l", nvmlMockSelector)).To(Succeed())
			var nodes []string
			for _, pod := range pods.Items {
				if metav1.IsControlledBy(&pod, &ds) {
					nodes = append(nodes, pod.Spec.NodeName)
				}
			}
			g.Expect(nodes).To(ConsistOf(realName))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
	})
})
