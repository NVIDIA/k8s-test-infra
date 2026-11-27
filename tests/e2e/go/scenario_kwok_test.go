//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package e2e

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
)

var _ = Describe("KWOK rack binding", Label("kwok", "kwok-binding"), func() {
	It("binds each node to its rack type and advertises the virtual GPUs to the scheduler", func(ctx SpecContext) {
		h := setupCluster(ctx, "kwok")
		realName := config.ClusterName() + "-worker"
		const virtualName = "kwok-worker"
		var cpuNode, kwokNode corev1.Node
		var virtualRack mokkav1.SGPURack
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
					virtualRack = rack
				}
				g.Expect(slot.BoundTo(expected.Name, expected.UID)).To(BeTrue(), "rack %s has nodeRef %+v", rack.Name, slot.NodeRef)
				bound[rack.Spec.NodeType] = rack.Spec.Identity.RackGroup
			}
			g.Expect(bound).To(Equal(map[mokkav1.NodeType]string{mokkav1.NodeTypeCPU: "real", mokkav1.NodeTypeKWOK: "virtual"}))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())

		By("reading all five hardware labels from the virtual Node")
		Eventually(func(g Gomega) {
			g.Expect(h.Kube.GetJSON(ctx, &kwokNode, "node", virtualName)).To(Succeed())
			g.Expect(kwokNode.Labels).To(HaveKeyWithValue("nvidia.com/gpu.product", "NVIDIA-A100-SXM4-40GB"))
			g.Expect(kwokNode.Labels).To(HaveKeyWithValue("nvidia.com/gpu.count", "8"))
			g.Expect(kwokNode.Labels).To(HaveKeyWithValue("nvidia.com/gpu.memory", "40960"))
			g.Expect(kwokNode.Labels).To(HaveKeyWithValue("nvidia.com/mig.strategy", "none"))
			g.Expect(kwokNode.Labels).To(HaveKeyWithValue("feature.node.kubernetes.io/pci-10de.present", "true"))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())

		By("pausing the data plane after it publishes node capacity")
		Eventually(func(g Gomega) {
			g.Expect(h.Kube.GetJSON(ctx, &kwokNode, "node", virtualName)).To(Succeed())
			for _, resources := range []corev1.ResourceList{kwokNode.Status.Capacity, kwokNode.Status.Allocatable} {
				gpus := resources["nvidia.com/gpu"]
				g.Expect(gpus.Value()).To(Equal(int64(8)))
			}
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())

		const dataPlane = "statefulset/mokka-kwok-data-plane"
		restore := func(ctx SpecContext) {
			out, err := h.Kube.KubectlCombined(ctx, "scale", dataPlane, "-n", nvmlMockNamespace, "--replicas=1")
			Expect(err).NotTo(HaveOccurred(), out)
			out, err = h.Kube.KubectlCombined(ctx, "rollout", "status", dataPlane, "-n", nvmlMockNamespace, "--timeout=2m")
			Expect(err).NotTo(HaveOccurred(), out)
		}
		DeferCleanup(restore)
		stop := func(ctx SpecContext) {
			out, err := h.Kube.KubectlCombined(ctx, "scale", dataPlane, "-n", nvmlMockNamespace, "--replicas=0")
			Expect(err).NotTo(HaveOccurred(), out)
			out, err = h.Kube.KubectlCombined(ctx, "wait", "--for=delete", "pod/mokka-kwok-data-plane-0", "-n", nvmlMockNamespace, "--timeout=2m")
			Expect(err).NotTo(HaveOccurred(), out)
		}
		stop(ctx)

		By("scheduling a one-GPU Pod while the data plane is stopped")
		probe := []byte(`apiVersion: v1
kind: Pod
metadata: {name: kwok-capacity-e2e, namespace: default}
spec:
  schedulerName: default-scheduler
  nodeSelector: {mokka.nvidia.com/pool: binding-e2e, mokka.nvidia.com/node-type: KWOK}
  tolerations: [{key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}]
  containers:
    - name: probe
      image: registry.k8s.io/pause:3.10
      resources: {limits: {nvidia.com/gpu: 1}}
`)
		Expect(h.Kube.Apply(ctx, probe)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, probe)).To(Succeed()) })
		By("binding a Pod with a resource claim and no whole-GPU request")
		claim := []byte(`apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata: {name: kwok-external-e2e, namespace: default}
spec: {devices: {}}
`)
		Expect(h.Kube.Apply(ctx, claim)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, claim)).To(Succeed()) })
		claimPod := []byte(`apiVersion: v1
kind: Pod
metadata: {name: kwok-claim-e2e, namespace: default}
spec:
  nodeName: kwok-worker
  resourceClaims: [{name: external, resourceClaimName: kwok-external-e2e}]
  containers:
    - name: probe
      image: registry.k8s.io/pause:3.10
      resources: {claims: [{name: external}]}
`)
		Expect(h.Kube.Apply(ctx, claimPod)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, claimPod)).To(Succeed()) })
		By("checking that KWOK can start an ordinary Pod while the GPU Pod waits")
		ordinary := []byte(`apiVersion: v1
kind: Pod
metadata: {name: kwok-ordinary-e2e, namespace: default}
spec:
  nodeName: kwok-worker
  containers: [{name: probe, image: registry.k8s.io/pause:3.10}]
`)
		Expect(h.Kube.Apply(ctx, ordinary)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, ordinary)).To(Succeed()) })
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", "kwok-ordinary-e2e", "-n", "default")).To(Succeed())
			g.Expect(pod.Status.Conditions).To(ContainElement(And(HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
			g.Expect(pod.Annotations).NotTo(HaveKey("mokka.nvidia.com/gpu-allocation"))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", "kwok-capacity-e2e", "-n", "default")).To(Succeed())
			g.Expect(pod.Spec.NodeName).To(Equal(virtualName))
			g.Expect(pod.Status.PodIP).NotTo(BeEmpty())
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
		Consistently(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", "kwok-capacity-e2e", "-n", "default")).To(Succeed())
			var claimed corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &claimed, "pod", "kwok-claim-e2e", "-n", "default")).To(Succeed())
			g.Expect(claimed.Status.Phase).To(Equal(corev1.PodPending))
			g.Expect(claimed.Annotations).NotTo(HaveKey("mokka.nvidia.com/gpu-allocation"))
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodPending))
			g.Expect(pod.Status.Conditions).NotTo(ContainElement(And(HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
			g.Expect(pod.Annotations).NotTo(HaveKey("mokka.nvidia.com/gpu-allocation"))
		}).WithContext(ctx).WithTimeout(3 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())

		By("resuming the data plane")
		restore(ctx)
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", "kwok-claim-e2e", "-n", "default")).To(Succeed())
			g.Expect(pod.Status.Conditions).To(ContainElement(And(HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
			g.Expect(pod.Annotations).To(HaveKeyWithValue("mokka.nvidia.com/gpu-allocation", "{}"))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
		By("checking the container allocation against the GPUs in its rack slot")
		var scheduled corev1.Pod
		Expect(h.Kube.GetJSON(ctx, &scheduled, "pod", "kwok-capacity-e2e", "-n", "default")).To(Succeed())
		slotGPUs := make([]string, 0, len(virtualRack.Spec.Nodes[0].GPUs))
		for _, gpu := range virtualRack.Spec.Nodes[0].GPUs {
			slotGPUs = append(slotGPUs, gpu.UUID)
		}
		Eventually(func(g Gomega) {
			var allocation mokkav1.SGPURackAllocation
			g.Expect(h.Kube.GetJSON(ctx, &allocation, "sgpurackallocation", virtualRack.Name)).To(Succeed())
			g.Expect(allocation.OwnedByRack(&virtualRack)).To(BeTrue())
			g.Expect(allocation.Nodes).To(HaveLen(1))
			n := allocation.Nodes[0]
			g.Expect(n.Name).To(Equal(virtualName))
			g.Expect(n.Pods).To(HaveLen(1))
			g.Expect(n.Pods[0].UID).To(Equal(scheduled.UID))
			g.Expect(n.Pods[0].Containers).To(HaveLen(1))
			c := n.Pods[0].Containers[0]
			g.Expect(c.Name).To(Equal("probe"))
			g.Expect(c.GPUs).To(HaveLen(1))
			g.Expect(slotGPUs).To(ContainElement(c.GPUs[0]))
			g.Expect(h.Kube.GetJSON(ctx, &scheduled, "pod", scheduled.Name, "-n", "default")).To(Succeed())
			g.Expect(scheduled.Status.Conditions).To(ContainElement(And(HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
			var annotated map[string][]string
			g.Expect(json.Unmarshal([]byte(scheduled.Annotations["mokka.nvidia.com/gpu-allocation"]), &annotated)).To(Succeed())
			g.Expect(annotated).To(Equal(map[string][]string{c.Name: c.GPUs}))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
		Expect(h.Kube.Delete(ctx, probe)).To(Succeed())
		Eventually(func(g Gomega) {
			var allocation mokkav1.SGPURackAllocation
			g.Expect(h.Kube.GetJSON(ctx, &allocation, "sgpurackallocation", virtualRack.Name)).To(Succeed())
			g.Expect(allocation.Nodes).To(BeEmpty())
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())

		By("rejecting a bound Pod that requests more GPUs than its node owns")
		oversized := []byte(`apiVersion: v1
kind: Pod
metadata: {name: kwok-admission-shortage, namespace: default}
spec:
  nodeName: kwok-worker
  containers:
    - name: trainer
      image: registry.k8s.io/pause:3.10
      resources: {limits: {nvidia.com/gpu: 9}}
`)
		Expect(h.Kube.Apply(ctx, oversized)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(h.Kube.Delete(ctx, oversized)).To(Succeed()) })
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(h.Kube.GetJSON(ctx, &pod, "pod", "kwok-admission-shortage", "-n", "default")).To(Succeed())
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodFailed))
			g.Expect(pod.Status.Reason).To(Equal("UnexpectedAdmissionError"))
			g.Expect(pod.Status.PodIP).NotTo(BeEmpty())
			g.Expect(pod.Status.Message).To(Equal("Pod was rejected: Allocate failed due to requested number of devices unavailable for nvidia.com/gpu. Requested: 9, Available: 8, which is unexpected"))
			g.Expect(pod.Annotations).NotTo(HaveKey("mokka.nvidia.com/gpu-allocation"))
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
		By("deleting the inventory while a live Pod still holds a GPU and the data plane is down")
		Expect(h.Kube.Apply(ctx, probe)).To(Succeed())
		Eventually(func(g Gomega) {
			var record mokkav1.SGPURackAllocation
			g.Expect(h.Kube.GetJSON(ctx, &record, "sgpurackallocation", virtualRack.Name)).To(Succeed())
			g.Expect(record.Nodes).To(HaveLen(1))
			g.Expect(record.Nodes[0].Pods).To(HaveLen(1))
		}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())

		stop(ctx)
		out, err := h.Kube.KubectlCombined(ctx, "delete", "sgpuinventory/binding-e2e", "--wait=false")
		Expect(err).NotTo(HaveOccurred(), out)
		checkHeld := func(g Gomega) {
			var rack mokkav1.SGPURack
			var node corev1.Node
			var record mokkav1.SGPURackAllocation
			g.Expect(h.Kube.GetJSON(ctx, &rack, "sgpurack", virtualRack.Name)).To(Succeed())
			g.Expect(rack.Status.Delivery.ReleaseRevision(kwokNode.UID)).To(BeNumerically(">", 0))
			g.Expect(rack.Spec.Nodes[0].BoundTo(virtualName, kwokNode.UID)).To(BeTrue())
			g.Expect(h.Kube.GetJSON(ctx, &record, "sgpurackallocation", virtualRack.Name)).To(Succeed())
			g.Expect(record.Nodes).To(HaveLen(1))
			g.Expect(h.Kube.GetJSON(ctx, &node, "node", virtualName)).To(Succeed())
			g.Expect(node.Labels).NotTo(HaveKey("mokka.nvidia.com/sgpu-assigned"))
			g.Expect(node.Status.Allocatable).To(HaveKey(corev1.ResourceName("nvidia.com/gpu")))
		}
		Eventually(checkHeld).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
		Consistently(checkHeld).WithContext(ctx).WithTimeout(3 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())

		By("resuming cleanup and keeping the source Node after the inventory disappears")
		restore(ctx)
		for _, object := range []string{"sgpuinventory/binding-e2e", "sgpurack/" + virtualRack.Name, "sgpurackallocation/" + virtualRack.Name} {
			out, err = h.Kube.KubectlCombined(ctx, "wait", "--for=delete", object, "--timeout=2m")
			Expect(err).NotTo(HaveOccurred(), out)
		}
		var released corev1.Node
		Expect(h.Kube.GetJSON(ctx, &released, "node", virtualName)).To(Succeed())
		Expect(released.UID).To(Equal(kwokNode.UID))
		Expect(released.Status.Capacity).NotTo(HaveKey(corev1.ResourceName("nvidia.com/gpu")))
		Expect(released.Status.Allocatable).NotTo(HaveKey(corev1.ResourceName("nvidia.com/gpu")))
		for _, key := range metadata.DiscoveryLabelKeys() {
			Expect(released.Labels).NotTo(HaveKey(key))
		}

	})
})
