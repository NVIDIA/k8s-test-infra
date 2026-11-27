// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
)

func TestPublishGPUCapacity(t *testing.T) {
	t.Parallel()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "virtual", UID: "node-uid", ResourceVersion: "42"}}
	node.Status.Capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}
	kube := kubefake.NewSimpleClientset(node)
	recorder := record.NewFakeRecorder(1)
	p := labelPublisher{nodes: kube.CoreV1().Nodes(), recorder: recorder}
	_, err := p.publishCapacity(t.Context(), node, "8")
	require.NoError(t, err)
	require.Len(t, kube.Actions(), 1)
	action := kube.Actions()[0].(clienttesting.PatchAction)
	require.Equal(t, "status", action.GetSubresource())
	require.JSONEq(t, `{"metadata":{"uid":"node-uid","resourceVersion":"42"},"status":{"capacity":{"nvidia.com/gpu":"8"},"allocatable":{"nvidia.com/gpu":"8"}}}`, string(action.GetPatch()))

	kube.ClearActions()
	node.Status.Capacity[gpuResource] = resource.MustParse("4")
	_, err = p.publishCapacity(t.Context(), node, "8")
	require.NoError(t, err)
	require.Empty(t, kube.Actions())
	select {
	case event := <-recorder.Events:
		require.Contains(t, event, "GPUCapacityConflict")
	default:
		t.Fatal("expected a conflict event")
	}
}

func TestResizeAndReleaseOwnedGPUStatus(t *testing.T) {
	t.Parallel()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "virtual", UID: "node-uid", ResourceVersion: "42"}}
	statusFields := metav1.ManagedFieldsEntry{Manager: fieldManager, Subresource: "status", FieldsV1: metav1.NewFieldsV1(
		`{"f:status":{"f:allocatable":{"f:nvidia.com/gpu":{}},"f:capacity":{"f:nvidia.com/gpu":{}}}}`)}
	node.ManagedFields = []metav1.ManagedFieldsEntry{statusFields}
	node.Status.Capacity = corev1.ResourceList{gpuResource: resource.MustParse("8")}
	node.Status.Allocatable = node.Status.Capacity.DeepCopy()
	kube := kubefake.NewSimpleClientset(node)
	p := labelPublisher{nodes: kube.CoreV1().Nodes(), recorder: record.NewFakeRecorder(1)}
	_, err := p.publishCapacity(t.Context(), node, "8")
	require.NoError(t, err)
	require.Empty(t, kube.Actions())
	_, err = p.publishCapacity(t.Context(), node, "4")
	require.NoError(t, err)
	require.Len(t, kube.Actions(), 1)
	require.JSONEq(t, `{"metadata":{"uid":"node-uid","resourceVersion":"42"},"status":{"capacity":{"nvidia.com/gpu":"4"},"allocatable":{"nvidia.com/gpu":"4"}}}`, string(kube.Actions()[0].(clienttesting.PatchAction).GetPatch()))

	// On release the external source keeps its share of capacity.
	node.ManagedFields = append(node.ManagedFields, metav1.ManagedFieldsEntry{Manager: "source", Subresource: "status",
		FieldsV1: metav1.NewFieldsV1(`{"f:status":{"f:capacity":{"f:nvidia.com/gpu":{}}}}`)})
	kube.ClearActions()
	_, err = p.publishCapacity(t.Context(), node, "")
	require.NoError(t, err)
	require.Len(t, kube.Actions(), 1)
	require.JSONEq(t, `{"metadata":{"uid":"node-uid","resourceVersion":"42"},"status":{"allocatable":{"nvidia.com/gpu":null}}}`, string(kube.Actions()[0].(clienttesting.PatchAction).GetPatch()))
}
