// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
)

func channelConfig(id string, source resourcev1.AllocationConfigSource) resourcev1.DeviceAllocationConfiguration {
	return resourcev1.DeviceAllocationConfiguration{Source: source, Requests: []string{"channel"}, DeviceConfiguration: resourcev1.DeviceConfiguration{Opaque: &resourcev1.OpaqueDeviceConfiguration{
		Driver: domainDriver, Parameters: runtime.RawExtension{Raw: []byte(fmt.Sprintf(`{"apiVersion":"resource.nvidia.com/v1beta1","kind":"ComputeDomainChannelConfig","domainID":%q}`, id))},
	}}}
}

func TestSharedChannelLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	first, second := boundTestPod("first", "worker"), boundTestPod("second", "worker")
	for _, pod := range []*corev1.Pod{first, second} {
		pod.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "shared", ResourceClaimName: ptr.To("channel")}}
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "worker-uid"}}
	kube := fake.NewClientset(node)
	pods := newShardPods()
	t.Cleanup(pods.allocations.ShutDown)
	t.Cleanup(pods.backfills.ShutDown)
	pods.setRack("rack", sets.New("worker"))
	claims := &shardClaims{pods: pods, client: kube.ResourceV1(), locations: make(map[cache.ObjectName]sets.Set[string])}
	domain := newDomainChannels(nil, claims)
	p := &labelPublisher{nodes: kube.CoreV1().Nodes(), slices: &nodeSlices{domains: domain}}
	claim := &resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "channel", Namespace: "team"}, Status: resourcev1.ResourceClaimStatus{
		Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{Results: []resourcev1.DeviceRequestAllocationResult{{Driver: domainDriver, Pool: "worker", Device: "channel-0", Request: "channel"}}, Config: []resourcev1.DeviceAllocationConfiguration{channelConfig("expected", resourcev1.AllocationConfigSourceClaim), channelConfig("class-default", resourcev1.AllocationConfigSourceClass)}}},
	}}
	for _, pod := range []*corev1.Pod{first, second} {
		claim.Status.ReservedFor = append(claim.Status.ReservedFor, resourcev1.ResourceClaimConsumerReference{Resource: "pods", Name: pod.Name, UID: pod.UID})
	}
	require.NoError(t, claims.Add(claim))
	live := map[types.UID]*corev1.Pod{first.UID: first, second.UID: second}
	verify := func(want string) {
		t.Helper()
		node, err := p.nodes.Get(ctx, node.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.NoError(t, p.preparePods(ctx, node, live))
		node, err = p.nodes.Get(ctx, node.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, want, node.Labels[domainLabel])
	}
	addDomain := func(ns string) {
		require.NoError(t, domain.watch.GetStore().Add(&metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: ns, UID: "expected"}}))
	}
	addDomain("another-team")
	verify("")
	addDomain("team")
	verify("expected")
	delete(live, first.UID)
	verify("expected")
	delete(live, second.UID)
	verify("")
}

func TestDomainSliceBlocksNodeReleaseInBothGPUModes(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"", "gpu.nvidia.com"} {
		t.Run("gpu-driver="+driver, func(t *testing.T) {
			t.Parallel()
			now := metav1.Now()
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "old-node", Labels: map[string]string{domainLabel: "domain-uid"}}}
			slice := &resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: domainSliceName(node.UID), DeletionTimestamp: &now, Finalizers: []string{"test.example/hold"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: node.Name, UID: node.UID, Controller: ptr.To(true)}}}}
			kube := fake.NewSimpleClientset(node, slice)
			p := labelPublisher{nodes: kube.CoreV1().Nodes(), dra: driver != "", slices: &nodeSlices{driver: driver, domains: &domainChannels{}, client: kube.ResourceV1().ResourceSlices()}}
			_, err := p.withdrawPools(t.Context(), node)
			require.ErrorContains(t, err, "waiting for deletion")
			for _, action := range kube.Actions() {
				require.Equal(t, "get", action.GetVerb())
				require.Equal(t, "resourceslices", action.GetResource().Resource)
			}
		})
	}
}
