// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package assignment

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

func TestEncodeAssignmentPreservesWireFormat(t *testing.T) {
	t.Parallel()

	rack := testRack()
	encoded, err := EncodeAssignment(rack, &rack.Spec.Nodes[0])
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"inventory":{"name":"inventory","uid":"inventory-uid"},"rack":{"name":"rack","uid":"rack-uid"},"profile":{"name":"profile","uid":"profile-uid","revision":"revision"},"rackGroup":"group","rackIndex":2,"nodeIndex":3,"nodeUID":"node-uid"}`, encoded) //nolint:testifylint // The wire encoding is a byte-for-byte contract.

	decoded, err := DecodeAssignment(encoded)
	require.NoError(t, err)
	require.Equal(t, AssignmentVersion, decoded.Version)
	require.True(t, decoded.Assigned)
	require.Equal(t, 1, AssignmentVersion)
}

func TestDecodeAssignmentRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	_, err := DecodeAssignment(`{"v":3}`)
	require.EqualError(t, err, "unsupported Node assignment version 3")
}

func TestEncodeAssignmentRequiresExactIdentities(t *testing.T) {
	t.Parallel()

	rack := testRack()
	tests := []struct {
		name string
		rack *mokkav1alpha1.SGPURack
		slot *mokkav1alpha1.SGPURackNode
	}{
		{name: "nil rack", slot: &rack.Spec.Nodes[0]},
		{name: "rack name", rack: func() *mokkav1alpha1.SGPURack { value := rack.DeepCopy(); value.Name = ""; return value }(), slot: &rack.Spec.Nodes[0]},
		{name: "rack UID", rack: func() *mokkav1alpha1.SGPURack { value := rack.DeepCopy(); value.UID = ""; return value }(), slot: &rack.Spec.Nodes[0]},
		{name: "nil slot", rack: rack},
		{name: "nil Node reference", rack: rack, slot: &mokkav1alpha1.SGPURackNode{}},
		{name: "Node UID", rack: rack, slot: &mokkav1alpha1.SGPURackNode{NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := EncodeAssignment(test.rack, test.slot)
			require.EqualError(t, err, "assignment requires exact rack, logical Node, and Kubernetes Node identities")
		})
	}
}

func testRack() *mokkav1alpha1.SGPURack {
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: types.UID("rack-uid")},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: "inventory", UID: types.UID("inventory-uid")},
			ProfileRef:   mokkav1alpha1.SGPURackProfileReference{Name: "profile", UID: types.UID("profile-uid"), Revision: "revision"},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 2},
			Nodes: []mokkav1alpha1.SGPURackNode{{
				Index:   3,
				NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: types.UID("node-uid")},
			}},
		},
	}
}

func TestPublishedAssignmentAndRelease(t *testing.T) {
	t.Parallel()
	binding := &PublishedBinding{Rack: ObjectReference{Name: "rack", UID: "rack-id"}, RackGroup: "group", RackDeliveryGenerationFloor: 12}
	bound, err := EncodePublication("node-id", 7, binding)
	require.NoError(t, err)
	require.JSONEq(t, `{"v":2,"nodeUID":"node-id","assignmentRevision":7,"assigned":true,"rack":{"name":"rack","uid":"rack-id"},"rackGroup":"group","rackIndex":0,"nodeIndex":0,"rackDeliveryGenerationFloor":12}`, bound)
	a, err := DecodeAssignment(bound)
	require.NoError(t, err)
	require.True(t, a.Assigned)
	require.Equal(t, int64(12), a.RackDeliveryGenerationFloor)
	require.Equal(t, binding.Rack, a.Rack)
	released, err := EncodePublication("node-id", 8, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"v":2,"nodeUID":"node-id","assignmentRevision":8,"assigned":false}`, released)
	a, err = DecodeAssignment(released)
	require.NoError(t, err)
	require.False(t, a.Assigned)
	require.Equal(t, int64(8), a.AssignmentRevision)
	require.Zero(t, a.Rack)
	_, err = DecodeAssignment(`{"v":2,"assigned":true,"nodeUID":"n","assignmentRevision":1,"rack":{"name":"r","uid":"r"},"rackGroup":"g","rackDeliveryGenerationFloor":1}`)
	require.Error(t, err)
	_, err = DecodeAssignment(`{"v":2,"nodeUID":"node-id","assignmentRevision":8}`)
	require.Error(t, err)
}
