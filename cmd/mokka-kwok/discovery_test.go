// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"encoding/json"
	"testing"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
)

func TestNodeWatchInitialListCanTransformTwice(t *testing.T) {
	t.Parallel()
	node := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "node-uid", ResourceVersion: "42"}}
	first, err := observeNode(node)
	require.NoError(t, err)
	second, err := observeNode(first)
	require.NoError(t, err)
	require.Same(t, first, second)
}

func TestConflictOnOneLabelPreservesOtherLabels(t *testing.T) {
	t.Parallel()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "virtual", Labels: map[string]string{
		metadata.GPUProductLabel: "from-source", metadata.GPUCountLabel: "8",
	}, ManagedFields: []metav1.ManagedFieldsEntry{{
		Manager: fieldManager, Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1", FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(`{"f:metadata":{"f:labels":{"f:nvidia.com/gpu.count":{}}}}`),
	}}}}
	kube := kubefake.NewSimpleClientset(node)
	var applied struct{ Metadata metav1.ObjectMeta }
	kube.PrependReactor("patch", "nodes", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, node, json.Unmarshal(action.(clienttesting.PatchAction).GetPatch(), &applied)
	})
	recorder := record.NewFakeRecorder(1)
	p := labelPublisher{nodes: kube.CoreV1().Nodes(), recorder: recorder}
	desired := map[string]string{metadata.GPUProductLabel: "NVIDIA-A100", metadata.GPUCountLabel: "8", metadata.GPUMemoryLabel: "40960"}
	require.NoError(t, p.publish(t.Context(), node, desired))
	require.Equal(t, map[string]string{metadata.GPUCountLabel: "8", metadata.GPUMemoryLabel: "40960"}, applied.Metadata.Labels)
	select {
	case event := <-recorder.Events:
		require.Contains(t, event, "DiscoveryLabelConflict")
	default:
		t.Fatal("expected a conflict Event")
	}
}

func TestPublishRequiresExactKWOKBinding(t *testing.T) {
	t.Parallel()
	rack := &mokkav1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid"}}
	rack.Spec.NodeType = mokkav1.NodeTypeKWOK
	rack.Generation = 1
	rack.Status.Delivery = &mokkav1.RackDelivery{Generation: 4, RackSpecGeneration: 1}
	rack.Spec.Identity.RackGroup = "compute"
	rack.Spec.Nodes = []mokkav1.SGPURackNode{{NodeRef: &mokkav1.SGPUNodeReference{Name: "node", UID: "node-uid", AssignmentRevision: 2}}}
	raw, err := assignment.EncodeAssignment(rack, &rack.Spec.Nodes[0])
	require.NoError(t, err)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "node-uid", Labels: map[string]string{metadata.AssignedLabel: "true"}, Annotations: map[string]string{metadata.AssignmentAnnotation: raw}}}
	views := newRackViews(newShardPods(), func(string) {})
	nodeState := state.NodePayload{Hardware: state.Hardware{Rack: state.Object{Name: rack.Name, UID: rack.UID}, RackDeliveryGeneration: 4, GPUModel: state.GPUModel{Memory: "40Gi", ProductName: "NVIDIA A100"}}, Slot: state.Slot{Node: state.Object{Name: "node", UID: node.UID}, AssignmentRevision: 2, GPUs: make([]state.GPU, 8)}}
	views.nodes[node.UID] = nodeState
	p := labelPublisher{views: views}
	labels, err := p.desiredLabels(node)
	require.NoError(t, err)
	require.Equal(t, "8", labels[metadata.GPUCountLabel])
	require.Equal(t, "40960", labels[metadata.GPUMemoryLabel])
	nodeState.RackDeliveryGeneration = 3
	views.nodes[node.UID] = nodeState
	labels, err = p.desiredLabels(node)
	require.NoError(t, err)
	require.Nil(t, labels)
	node.UID = "replacement"
	labels, err = p.desiredLabels(node)
	require.NoError(t, err)
	require.Nil(t, labels)
}
