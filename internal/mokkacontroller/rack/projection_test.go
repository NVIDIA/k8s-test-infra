// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/materialize"
	"github.com/stretchr/testify/require"
)

func TestProjectionTargetAllowedRequiresOwnedDesiredEligibleBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*mokkav1alpha1.SGPURack, *corev1.Node)
		want   bool
	}{
		{name: "owned desired eligible binding", want: true},
		{
			name: "forged controller reference without SSA ownership",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				rack.ManagedFields = nil
			},
		},
		{
			name: "binding owned by a foreign field manager",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				setRackBindingManagedFields(t, rack, "foreign-controller", rack.Spec.Nodes[0].Index)
			},
		},
		{
			name: "binding co-owned by a foreign field manager",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				foreign := rack.ManagedFields[0]
				foreign.Manager = "foreign-controller"
				rack.ManagedFields = append(rack.ManagedFields, foreign)
			},
		},
		{
			name: "foreign manager owns unrelated metadata",
			want: true,
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				foreign := rack.ManagedFields[0]
				foreign.Manager = "foreign-controller"
				foreign.FieldsV1 = metav1.NewFieldsV1(`{"f:metadata":{"f:labels":{"f:example.com/user":{}}}}`)
				rack.ManagedFields = append(rack.ManagedFields, foreign)
			},
		},
		{
			name: "foreign rack",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				rack.OwnerReferences = nil
			},
		},
		{
			name: "forged owner and inventory reference",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				rack.OwnerReferences[0].UID = "forged-inventory-uid"
				rack.Spec.InventoryRef.UID = "forged-inventory-uid"
			},
		},
		{
			name: "foreign patch redirects to arbitrary eligible Node",
			mutate: func(rack *mokkav1alpha1.SGPURack, node *corev1.Node) {
				node.Name, node.UID = "arbitrary-node", "arbitrary-node-uid"
				rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}
				setRackBindingManagedFields(t, rack, "foreign-controller", rack.Spec.Nodes[0].Index)
			},
		},
		{
			name: "undesired rack template",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				rack.Spec.Identity.FabricUUID = "caller-controlled"
			},
		},
		{
			name: "undesired rack coordinate",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				rack.Spec.Identity.RackIndex = 1
			},
		},
		{
			name: "deleting rack",
			mutate: func(rack *mokkav1alpha1.SGPURack, _ *corev1.Node) {
				now := metav1.Now()
				rack.DeletionTimestamp = &now
			},
		},
		{
			name: "ineligible Node",
			mutate: func(_ *mokkav1alpha1.SGPURack, node *corev1.Node) {
				delete(node.Labels, allocate.EligibleNodeLabel)
			},
		},
		{
			name: "terminating Node",
			mutate: func(_ *mokkav1alpha1.SGPURack, node *corev1.Node) {
				now := metav1.Now()
				node.DeletionTimestamp = &now
			},
		},
		{
			name: "selector-mismatched Node",
			mutate: func(_ *mokkav1alpha1.SGPURack, node *corev1.Node) {
				node.Labels["pool"] = "other"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, inventory, keys := allocationScaleSource(1, 1)
			key := keys[0]
			profile := source.profiles[inventory.Spec.RackGroups[0].ProfileRef.Name]
			rendered, err := materialize.RenderRack(materialize.RackInput{
				InventoryName: inventory.Name,
				InventoryUID:  inventory.UID,
				Group:         inventory.Spec.RackGroups[0],
				RackIndex:     0,
				Profile:       profile,
			})
			require.NoError(t, err)
			rack := newRack(inventory, rendered.Name, rendered.Spec)
			rack.UID = "rack-uid"
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: source.nodes[0].Name, UID: source.nodes[0].UID,
				Labels: map[string]string{
					allocate.EligibleNodeLabel: "true",
					"pool":                     key.RackGroup,
				},
			}}
			rack.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: node.Name, UID: node.UID}
			setRackBindingManagedFields(t, rack, RackFieldManager, rack.Spec.Nodes[0].Index)
			rack.ManagedFields[0].Operation = metav1.ManagedFieldsOperationUpdate
			if tt.mutate != nil {
				tt.mutate(rack, node)
			}

			allowed, err := ProjectionTargetAllowed(source, rack, &rack.Spec.Nodes[0], node)

			require.NoError(t, err)
			require.Equal(t, tt.want, allowed)
		})
	}
}

func setRackBindingManagedFields(
	t *testing.T,
	rack *mokkav1alpha1.SGPURack,
	manager string,
	index int32,
) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"f:spec": map[string]any{
			"f:nodes": map[string]any{
				fmt.Sprintf("k:{\"index\":%d}", index): map[string]any{
					"f:nodeRef": map[string]any{
						"f:name": map[string]any{},
						"f:uid":  map[string]any{},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	rack.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: manager, Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(string(raw)),
	}}
}
