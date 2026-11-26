// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
)

func TestDRACopiesContainerRequests(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pod := boundTestPod("work", "worker")
	pod.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "own", ResourceClaimName: ptr.To("named")}, {Name: "generated", ResourceClaimTemplateName: ptr.To("template")}}
	pod.Status.ResourceClaimStatuses = []corev1.PodResourceClaimStatus{{Name: "generated", ResourceClaimName: ptr.To("from-template")}}
	pod.Spec.InitContainers = []corev1.Container{{Name: "setup", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "generated"}}}}}
	pod.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "own", Request: "fast"}}
	pod.Status.ExtendedResourceClaimStatus = &corev1.PodExtendedResourceClaimStatus{ResourceClaimName: "implicit", RequestMappings: []corev1.ContainerExtendedResourceRequest{
		{ContainerName: "main", ResourceName: string(gpuResource), RequestName: "app-gpu"},
		{ContainerName: "main", ResourceName: string(gpuResource), RequestName: "reused-gpu"},
	}}
	f := newAllocationFixture(t, nil, map[string][]string{"worker": {"A", "B", "C"}}, pod)
	require.NoError(t, f.kube.Tracker().Add(pod))
	f.allocator.dra = true
	slot := f.allocator.views.nodes["worker-uid"]
	for i := range slot.GPUs {
		slot.GPUs[i].MinorNumber = int32(i)
	}
	pods := f.allocator.views.pods
	store := &shardClaims{pods: pods, client: f.kube.ResourceV1(), driver: "gpu.example.com", locations: make(map[cache.ObjectName]sets.Set[string])}
	f.allocator.claims = store
	pods.claims = store
	result := func(req, dev string) resourcev1.DeviceRequestAllocationResult {
		return resourcev1.DeviceRequestAllocationResult{Driver: store.driver, Pool: "worker", Request: req, Device: dev}
	}
	claim := func(name string, results ...resourcev1.DeviceRequestAllocationResult) *resourcev1.ResourceClaim {
		return &resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: name}, Status: resourcev1.ResourceClaimStatus{ReservedFor: []resourcev1.ResourceClaimConsumerReference{{Resource: "pods", Name: pod.Name, UID: pod.UID}}, Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{Results: results}}}}
	}
	foreign := resourcev1.DeviceRequestAllocationResult{Driver: "network.example.com", Pool: "foreign-pool", Device: "nic-0", Request: "fast/first"}
	explicit := claim("named", result("fast/first", "gpu-2"), result("unused", "gpu-1"), foreign)
	extended := claim("implicit", result("app-gpu", "gpu-0"), result("reused-gpu", "gpu-1"))
	generated := claim("from-template", result("setup", "gpu-1"))
	// The initial list only retains allocations on shard Nodes.
	unrelated := claim("unrelated", foreign)
	require.NoError(t, store.Replace([]any{explicit, extended, unrelated}, "1"))
	require.Len(t, store.locations, 2)
	// The Node was acquired after the template claim's event. Fetch it by name.
	require.NoError(t, f.kube.Tracker().Add(generated))
	require.NoError(t, f.allocator.reconcile(ctx, f.rack.Name))
	rows := f.record(t)
	require.Len(t, rows, 1)
	require.Len(t, rows[0].Pods, 1)
	got := rows[0].Pods[0].Containers
	require.Len(t, got, 2)
	require.Equal(t, "setup", got[0].Name)
	require.Equal(t, []string{"B"}, got[0].GPUs)
	require.Equal(t, []string{"A", "B", "C"}, got[1].GPUs)
	live, err := f.kube.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.JSONEq(t, `{"setup":["B"],"main":["A","B","C"]}`, live.Annotations[gpuAllocationAnnotation])
	// Losing the reservation invalidates the whole grant. Reconcile the change
	// even if a replacement Pod uses the same name.
	require.NoError(t, pods.Update(live))
	stale := extended.DeepCopy()
	stale.Status.ReservedFor[0].UID = "replacement"
	require.NoError(t, store.Update(stale))
	require.NoError(t, f.allocator.reconcile(ctx, f.rack.Name))
	require.Empty(t, f.record(t))
	live, err = f.kube.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotContains(t, live.Annotations, gpuAllocationAnnotation)
	pods.setRack(f.rack.Name, nil)
	require.Empty(t, store.locations)
}

func TestDRAForeignOnlyAndWrongPool(t *testing.T) {
	t.Parallel()
	pod := boundTestPod("work", "worker")
	claim := &resourcev1.ResourceClaim{Status: resourcev1.ResourceClaimStatus{
		ReservedFor: []resourcev1.ResourceClaimConsumerReference{{Resource: "pods", Name: pod.Name, UID: pod.UID}},
		Allocation:  &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "other", Pool: "worker", Device: "gpu-0"}}}}}}
	ids, ok := claimUUIDs(claim, pod, claimUse{}, "gpu.example.com", map[string]string{"gpu-0": "A"})
	require.True(t, ok)
	require.Empty(t, ids)
	pod.Spec.SchedulingGroup = &corev1.PodSchedulingGroup{PodGroupName: ptr.To("batch")}
	claim.Status.ReservedFor = []resourcev1.ResourceClaimConsumerReference{{APIGroup: "scheduling.k8s.io", Resource: "podgroups", Name: "batch", UID: "group-uid"}}
	require.True(t, reservedForPod(claim, pod))
	r := &claim.Status.Allocation.Devices.Results[0]
	r.Driver, r.Pool = "gpu.example.com", "different-node"
	_, ok = claimUUIDs(claim, pod, claimUse{}, "gpu.example.com", map[string]string{"gpu-0": "A"})
	require.False(t, ok)
}
