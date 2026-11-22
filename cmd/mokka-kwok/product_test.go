// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
)

func TestProductConflictPreservesSourceLabel(t *testing.T) {
	t.Parallel()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "virtual", Labels: map[string]string{productLabel: "from-source"}}}
	kube := kubefake.NewSimpleClientset(node)
	recorder := record.NewFakeRecorder(1)
	p := productPublisher{nodes: kube.CoreV1().Nodes(), recorder: recorder}
	require.NoError(t, p.publish(t.Context(), node, "NVIDIA-A100"))
	require.Empty(t, kube.Actions())
	select {
	case event := <-recorder.Events:
		require.Contains(t, event, "Warning GPUProductConflict")
	default:
		t.Fatal("expected a conflict Event")
	}
	require.NoError(t, p.publish(t.Context(), node, ""))
	require.Empty(t, kube.Actions())
}

func TestPublishRequiresExactKWOKBinding(t *testing.T) {
	t.Parallel()
	rack := &mokkav1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid"}}
	rack.Spec.NodeType = mokkav1.NodeTypeKWOK
	rack.Spec.ProfileRef = mokkav1.SGPURackProfileReference{Name: "a100", UID: "profile-uid", Revision: "revision"}
	rack.Spec.Nodes = []mokkav1.SGPURackNode{{NodeRef: &mokkav1.SGPUNodeReference{Name: "node", UID: "node-uid"}}}
	raw, err := assignment.EncodeAssignment(rack, &rack.Spec.Nodes[0])
	require.NoError(t, err)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "node-uid", Labels: map[string]string{metadata.AssignedLabel: "true"}, Annotations: map[string]string{metadata.AssignmentAnnotation: raw}}}
	ref, ok, err := projectedProfile(node, []any{rack})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, rack.Spec.ProfileRef, ref)
	node.UID = "replacement"
	_, ok, err = projectedProfile(node, []any{rack})
	require.NoError(t, err)
	require.False(t, ok)
}
