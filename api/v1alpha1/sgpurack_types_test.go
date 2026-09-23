// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

func TestSGPURackCarriesInventoryControllerOwnership(t *testing.T) {
	t.Parallel()

	inventory := &SGPUInventory{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "SGPUInventory",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "training",
			UID:  types.UID("inventory-uid"),
		},
	}
	ownerReference := metav1.NewControllerRef(inventory, GroupVersion.WithKind("SGPUInventory"))
	ownerReference.BlockOwnerDeletion = ptr.To(false)
	rack := &SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{*ownerReference},
		},
		Spec: SGPURackSpec{
			InventoryRef: SGPURackInventoryReference{
				Name: inventory.Name,
				UID:  inventory.UID,
			},
		},
	}

	owner := metav1.GetControllerOf(rack)
	require.NotNil(t, owner)
	require.Equal(t, GroupVersion.String(), owner.APIVersion)
	require.Equal(t, "SGPUInventory", owner.Kind)
	require.Equal(t, rack.Spec.InventoryRef.Name, owner.Name)
	require.Equal(t, rack.Spec.InventoryRef.UID, owner.UID)
	require.False(t, *owner.BlockOwnerDeletion)
}

func TestSGPURackDeepCopyDoesNotAliasBindings(t *testing.T) {
	t.Parallel()

	rack := &SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{UID: types.UID("inventory-uid")}},
		},
		Spec: SGPURackSpec{
			Nodes: []SGPURackNode{{
				Index: 0,
				NodeRef: &SGPUNodeReference{
					Name: "worker-0",
					UID:  types.UID("node-uid"),
				},
				GPUs: []SGPURackGPU{{Index: 0, Serial: "serial-0"}},
			}},
		},
		Status: SGPURackStatus{
			Conditions: []metav1.Condition{{Type: RackConditionReady}},
		},
	}

	clone := rack.DeepCopy()
	clone.OwnerReferences[0].UID = types.UID("different-inventory")
	clone.Spec.Nodes[0].NodeRef.UID = types.UID("different-node")
	clone.Spec.Nodes[0].GPUs[0].Serial = "different-serial"
	clone.Status.Conditions[0].Type = "Changed"

	require.Equal(t, types.UID("inventory-uid"), rack.OwnerReferences[0].UID)
	require.Equal(t, types.UID("node-uid"), rack.Spec.Nodes[0].NodeRef.UID)
	require.Equal(t, "serial-0", rack.Spec.Nodes[0].GPUs[0].Serial)
	require.Equal(t, RackConditionReady, rack.Status.Conditions[0].Type)
}
