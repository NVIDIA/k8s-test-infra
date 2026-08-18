// Copyright (c) 2026, NVIDIA CORPORATION. All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

func TestCheckAcceptsExactProjectionAndRejectsUIDDrift(t *testing.T) {
	o, inventory, racks, nodes := validFixture(t)

	got := check(o, inventory, racks, nodes)
	require.True(t, got.Success, got.Errors)
	require.NotEmpty(t, got.AssignmentDigest)
	require.Equal(t, 1, got.ProjectedNodes)

	var projected assignment
	require.NoError(t, json.Unmarshal([]byte(nodes.Items[0].Annotations[assignmentAnnotation]), &projected))
	projected.NodeUID = "replacement-uid"
	drifted, err := json.Marshal(projected)
	require.NoError(t, err)
	nodes.Items[0].Annotations[assignmentAnnotation] = string(drifted)

	got = check(o, inventory, racks, nodes)
	require.False(t, got.Success)
	require.Contains(t, got.Errors, `Node "mokka-node-000000" projection does not match its exact rack binding`)
}

func TestCheckRejectsReleasedProjectionKeysWithEmptyValues(t *testing.T) {
	o, inventory, racks, nodes := validFixture(t)
	o.expectedRacks = 0
	o.expectedAllocated = 0
	o.requestsSatisfied = false
	inventory.Status.Capacity = mokkav1alpha1.InventoryCapacity{}
	inventory.Status.Usage = mokkav1alpha1.InventoryUsage{RequestedNodes: 1, PendingNodes: 1}
	for i := range inventory.Status.Conditions {
		if inventory.Status.Conditions[i].Type == "RequestsSatisfied" {
			inventory.Status.Conditions[i].Status = metav1.ConditionFalse
		}
	}
	racks.Items = nil
	nodes.Items[0].Labels[assignedLabel] = ""
	nodes.Items[0].Annotations[assignmentAnnotation] = ""

	got := check(o, inventory, racks, nodes)
	require.False(t, got.Success)
	require.Contains(t, got.Errors, `Node "mokka-node-000000" retains released assignment metadata`)
}

func validFixture(t *testing.T) (options, *mokkav1alpha1.SGPUInventory, *mokkav1alpha1.SGPURackList, *corev1.NodeList) {
	t.Helper()
	const (
		inventoryUID = types.UID("inventory-uid")
		rackUID      = types.UID("rack-uid")
		nodeUID      = types.UID("node-uid")
	)
	conditions := func(types ...string) []metav1.Condition {
		result := make([]metav1.Condition, 0, len(types))
		for _, conditionType := range types {
			result = append(result, metav1.Condition{Type: conditionType, Status: metav1.ConditionTrue})
		}
		return result
	}
	inventory := &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{Name: "mokka-kwok", UID: inventoryUID, Generation: 1},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
			ID: "compute", Count: 1, ProfileRef: mokkav1alpha1.ProfileReference{Name: "mokka-kwok"},
		}}},
		Status: mokkav1alpha1.SGPUInventoryStatus{
			Capacity:   mokkav1alpha1.InventoryCapacity{Racks: 1, Nodes: 1, GPUs: 1},
			Usage:      mokkav1alpha1.InventoryUsage{RequestedNodes: 1, AllocatedNodes: 1},
			Conditions: conditions("Accepted", "ResolvedRefs", "Programmed", "RequestsSatisfied"),
		},
	}
	for index := range inventory.Status.Conditions {
		inventory.Status.Conditions[index].ObservedGeneration = inventory.Generation
	}
	rackName := materialize.RackName(inventory.Name, inventoryUID, "compute", 0)
	rack := mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: rackName, UID: rackUID, Generation: 1},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventoryUID},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "compute", RackIndex: 0},
			Slots: []mokkav1alpha1.SGPURackSlot{{
				Index: 0, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "mokka-node-000000", UID: nodeUID},
			}},
		},
		Status: mokkav1alpha1.SGPURackStatus{
			ObservedGeneration: 1, AssignedSlots: 1,
			Conditions: conditions("Ready"),
		},
	}
	encoded, err := json.Marshal(assignment{
		Version: 1, Inventory: objectReference{Name: inventory.Name, UID: inventoryUID},
		Rack: objectReference{Name: rackName, UID: rackUID}, RackGroup: "compute",
		RackIndex: 0, SlotIndex: 0, NodeUID: nodeUID,
	})
	require.NoError(t, err)
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "mokka-node-000000", UID: nodeUID,
		Labels: map[string]string{
			ownerLabel: "test-cluster", eligibleLabel: "true", assignedLabel: "true",
		},
		Annotations: map[string]string{assignmentAnnotation: string(encoded)},
	}}
	return options{
		state: "test", clusterLabel: "test-cluster", expectedRacks: 1, nodesPerRack: 1,
		expectedNodes: 1, expectedEligible: 1, expectedAllocated: 1, requestsSatisfied: true,
	}, inventory, &mokkav1alpha1.SGPURackList{Items: []mokkav1alpha1.SGPURack{rack}}, &corev1.NodeList{Items: []corev1.Node{node}}
}
