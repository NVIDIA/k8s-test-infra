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
	require.Equal(t, 1, AssignmentVersion)
}

func TestDecodeAssignmentRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	_, err := DecodeAssignment(`{"v":2}`)
	require.EqualError(t, err, "unsupported Node assignment version 2")
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
