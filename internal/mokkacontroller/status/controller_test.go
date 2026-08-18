// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package status

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
)

func TestComputeInventoryStatusExactAggregateMathAndConditions(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := aggregateInput()
	input.Inventory.Status.Conditions = []metav1.Condition{{
		Type: mokkav1alpha1.InventoryConditionAccepted, Status: metav1.ConditionTrue,
		Reason: ReasonAccepted, LastTransitionTime: metav1.NewTime(time.Unix(100, 0)),
	}}

	got := ComputeInventory(input, now)
	require.Equal(t, "a,b,missing", got.RackGroupsSummary)
	require.Equal(t, mokkav1alpha1.InventoryCapacity{Racks: 3, Nodes: 7, GPUs: 22}, got.Capacity)
	require.Equal(t, mokkav1alpha1.InventoryUsage{
		RequestedNodes: 5, AllocatedNodes: 5, AvailableNodes: 2,
		PendingNodes: 1,
	}, got.Usage)
	require.Equal(t, []mokkav1alpha1.RackGroupStatus{
		{ID: "a", ProfileName: "pa", Capacity: mokkav1alpha1.InventoryCapacity{Racks: 2, Nodes: 4, GPUs: 16}, Usage: mokkav1alpha1.InventoryUsage{
			RequestedNodes: 4, AllocatedNodes: 3, AvailableNodes: 1, PendingNodes: 1,
		}},
		{ID: "b", ProfileName: "pb", Capacity: mokkav1alpha1.InventoryCapacity{Racks: 1, Nodes: 3, GPUs: 6}, Usage: mokkav1alpha1.InventoryUsage{
			RequestedNodes: 2, AllocatedNodes: 2, AvailableNodes: 1,
		}},
		{ID: "missing", ProfileName: "missing"},
	}, got.RackGroups)

	accepted := condition(got.Conditions, mokkav1alpha1.InventoryConditionAccepted)
	require.Equal(t, metav1.ConditionTrue, accepted.Status)
	require.Equal(t, ReasonAccepted, accepted.Reason)
	require.Equal(t, metav1.NewTime(time.Unix(100, 0)), accepted.LastTransitionTime, "unchanged status/reason preserves transition time")
	require.Equal(t, input.Inventory.Generation, accepted.ObservedGeneration)
	require.Equal(t, metav1.ConditionFalse, condition(got.Conditions, mokkav1alpha1.InventoryConditionResolvedRefs).Status)
	require.Equal(t, ReasonProfileNotFound, condition(got.Conditions, mokkav1alpha1.InventoryConditionResolvedRefs).Reason)
	require.Equal(t, metav1.ConditionFalse, condition(got.Conditions, mokkav1alpha1.InventoryConditionProgrammed).Status)
	require.Equal(t, metav1.ConditionFalse, condition(got.Conditions, mokkav1alpha1.InventoryConditionRequestsSatisfied).Status)
	require.Equal(t, ReasonPlacementConflicts, condition(got.Conditions, mokkav1alpha1.InventoryConditionRequestsSatisfied).Reason)
	require.Len(t, got.Conditions, 4)
}

func TestComputeInventoryRequestedNodesAreDistinctWhileGroupsOverlap(t *testing.T) {
	input := aggregateInput()
	input.RackResult.ProfileIssues = nil
	input.RackResult.ResolvedRefs = true
	delete(input.Profiles, "missing")
	input.Inventory.Spec.RackGroups = input.Inventory.Spec.RackGroups[:2]
	input.Racks = nil
	input.Nodes = []*corev1.Node{node("overlap", "overlap-uid", true, "both")}
	input.RackResult.Allocation = allocate.Plan{Conflicts: []allocate.Conflict{{
		Kind:       allocate.ConflictSelectorOverlap,
		Node:       allocationNode(input.Nodes[0]),
		Candidates: []allocate.GroupKey{groupKey(input.Inventory, "a"), groupKey(input.Inventory, "b")},
	}}}

	got := ComputeInventory(input, metav1.Now())
	require.Equal(t, int32(1), got.Usage.RequestedNodes)
	require.Equal(t, int32(1), got.RackGroups[0].Usage.RequestedNodes)
	require.Equal(t, int32(1), got.RackGroups[1].Usage.RequestedNodes)
	require.Equal(t, ReasonPlacementConflicts, condition(got.Conditions, mokkav1alpha1.InventoryConditionRequestsSatisfied).Reason)
}

func TestComputeRackStatusCountsExactProjectionAndDuplicateBindings(t *testing.T) {
	input := aggregateInput()
	rack := input.Racks[0]
	oldTime := metav1.NewTime(time.Unix(50, 0))
	rack.Status.Conditions = []metav1.Condition{{
		Type: mokkav1alpha1.RackConditionReady, Status: metav1.ConditionFalse,
		Reason: ReasonDuplicateBindings, LastTransitionTime: oldTime,
	}}

	got := ComputeRack(RackInput{Rack: rack, Racks: input.Racks, Nodes: input.Nodes, Projection: input.Projection}, metav1.NewTime(time.Unix(100, 0)))
	require.Equal(t, int32(3), got.AssignedSlots)
	require.Equal(t, metav1.ConditionFalse, condition(got.Conditions, mokkav1alpha1.RackConditionReady).Status)
	require.Equal(t, ReasonDuplicateBindings, condition(got.Conditions, mokkav1alpha1.RackConditionReady).Reason)
	require.Equal(t, oldTime, condition(got.Conditions, mokkav1alpha1.RackConditionReady).LastTransitionTime)
	require.Len(t, got.Conditions, 1)

	clean := rack.DeepCopy()
	clean.Spec.Slots = clean.Spec.Slots[:1]
	clean.Status = mokkav1alpha1.SGPURackStatus{}
	got = ComputeRack(RackInput{Rack: clean, Racks: []*mokkav1alpha1.SGPURack{clean}, Nodes: input.Nodes, Projection: input.Projection}, metav1.Now())
	require.Equal(t, metav1.ConditionTrue, condition(got.Conditions, mokkav1alpha1.RackConditionReady).Status)
}

func TestStatusWritersSuppressIdenticalUpdatesAndRetryConflicts(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := aggregateInput()
	inventoryWriter := &fakeInventoryWriter{object: input.Inventory.DeepCopy(), conflictOnce: true}
	rackInput := RackInput{Rack: input.Racks[1], Racks: input.Racks, Nodes: input.Nodes, Projection: input.Projection}
	rackWriter := &fakeRackWriter{object: rackInput.Rack.DeepCopy(), conflictOnce: true}
	reconciler := NewReconciler(inventoryWriter, rackWriter, func() metav1.Time { return now })

	changed, err := reconciler.ReconcileInventory(context.Background(), input)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 2, inventoryWriter.updates)
	changed, err = reconciler.ReconcileInventory(context.Background(), input)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 2, inventoryWriter.updates)

	changed, err = reconciler.ReconcileRack(context.Background(), rackInput)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 2, rackWriter.updates)
	changed, err = reconciler.ReconcileRack(context.Background(), rackInput)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 2, rackWriter.updates)
}

func TestRackStatusTreatsStaleExactObjectAsConverged(t *testing.T) {
	input := aggregateInput()
	rackInput := RackInput{Rack: input.Racks[0], Racks: input.Racks, Nodes: input.Nodes, Projection: input.Projection}

	t.Run("deleted", func(t *testing.T) {
		writer := &fakeRackWriter{getErr: apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), rackInput.Rack.Name)}
		changed, err := NewReconciler(nil, writer, nil).ReconcileRack(context.Background(), rackInput)
		require.NoError(t, err)
		require.False(t, changed)
	})

	t.Run("deleted during update", func(t *testing.T) {
		writer := &fakeRackWriter{
			object:    rackInput.Rack.DeepCopy(),
			updateErr: apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), rackInput.Rack.Name),
		}
		changed, err := NewReconciler(nil, writer, nil).ReconcileRack(context.Background(), rackInput)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, 1, writer.updates)
	})

	t.Run("recreated", func(t *testing.T) {
		recreated := rackInput.Rack.DeepCopy()
		recreated.UID = "replacement-rack-uid"
		writer := &fakeRackWriter{object: recreated}
		changed, err := NewReconciler(nil, writer, nil).ReconcileRack(context.Background(), rackInput)
		require.NoError(t, err)
		require.False(t, changed)
		require.Zero(t, writer.updates)
	})
}

func TestProjectionErrorsRemainRetryableStatusInputs(t *testing.T) {
	input := aggregateInput()
	input.RackResult.ProfileIssues = nil
	input.RackResult.ResolvedRefs = true
	input.Inventory.Spec.RackGroups = input.Inventory.Spec.RackGroups[:2]
	input.Inventory.Spec.RackGroups[0].Count = 1
	input.Projection[0].State = controllerprojection.StateConflict
	input.Projection[0].Reason = controllerprojection.ReasonNodeMetadataConflict
	input.Projection[0].Message = "owned elsewhere"
	got := ComputeInventory(input, metav1.Now())
	programmed := condition(got.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, ReasonNodeMetadataConflict, programmed.Reason)
}

func TestInvalidInventoryStatusSurfacesStableValidationError(t *testing.T) {
	input := aggregateInput()
	input.RackResult.Accepted = false
	input.RackResult.ValidationError = `rack group "a" selector: selector must not reference controller-owned label "mokka.nvidia.com/sgpu-assigned"`

	got := ComputeInventory(input, metav1.Now())
	accepted := condition(got.Conditions, mokkav1alpha1.InventoryConditionAccepted)
	require.Equal(t, metav1.ConditionFalse, accepted.Status)
	require.Equal(t, ReasonInvalidInventory, accepted.Reason)
	require.Equal(t, input.RackResult.ValidationError, accepted.Message)
	materialized := condition(got.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
	require.Equal(t, metav1.ConditionFalse, materialized.Status)
	require.Equal(t, ReasonInvalidInventory, materialized.Reason)
}

func aggregateInput() InventoryInput {
	inventory := &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{Name: "inventory", UID: "inventory-uid", Generation: 7},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{
			{ID: "a", Count: 2, ProfileRef: mokkav1alpha1.ProfileReference{Name: "pa"}, Placement: placement("a", "both")},
			{ID: "b", Count: 1, ProfileRef: mokkav1alpha1.ProfileReference{Name: "pb"}, Placement: placement("b", "both")},
			{ID: "missing", Count: 4, ProfileRef: mokkav1alpha1.ProfileReference{Name: "missing"}, Placement: placement("missing")},
		}},
	}
	profiles := map[string]*mokkav1alpha1.SGPUProfile{
		"pa": profile("pa", 2, 4),
		"pb": profile("pb", 3, 2),
	}
	nodes := []*corev1.Node{
		node("overlap", "overlap-uid", true, "both"),
		node("a-projected", "a-projected-uid", true, "a"),
		node("a-duplicate", "a-duplicate-uid", true, "a"),
		node("a-pending", "a-pending-uid", true, "a"),
		node("b-projected", "b-projected-uid", true, "b"),
		node("bound-ineligible", "bound-ineligible-uid", false, "a"),
	}
	rackA := statusRack(inventory, "rack-a", "rack-a-uid", "a", 0, []nodeBinding{
		{name: "a-projected", uid: "a-projected-uid"},
		{name: "a-duplicate", uid: "a-duplicate-uid"},
		{name: "bound-ineligible", uid: "bound-ineligible-uid"},
	})
	rackB := statusRack(inventory, "rack-b", "rack-b-uid", "b", 0, []nodeBinding{
		{name: "b-projected", uid: "b-projected-uid"},
		{name: "a-duplicate", uid: "a-duplicate-uid"},
	})
	allocation := allocate.Plan{
		Pending: []allocate.Node{allocationNode(nodes[3])},
		Conflicts: []allocate.Conflict{
			{Kind: allocate.ConflictSelectorOverlap, Node: allocationNode(nodes[0]), Candidates: []allocate.GroupKey{groupKey(inventory, "a"), groupKey(inventory, "b")}},
			{Kind: allocate.ConflictDuplicateBinding, Node: allocationNode(nodes[2]), Bindings: []allocate.Binding{
				binding(inventory, "a", 0, 1, nodes[2]), binding(inventory, "b", 0, 1, nodes[2]),
			}},
		},
	}
	projection := []controllerprojection.Outcome{
		{RackName: rackA.Name, RackUID: rackA.UID, SlotIndex: 0, NodeName: nodes[1].Name, NodeUID: nodes[1].UID, State: controllerprojection.StateProjected},
		{RackName: rackB.Name, RackUID: rackB.UID, SlotIndex: 0, NodeName: nodes[4].Name, NodeUID: nodes[4].UID, State: controllerprojection.StateProjected},
		{RackName: rackA.Name, RackUID: rackA.UID, SlotIndex: 1, NodeName: nodes[2].Name, NodeUID: nodes[2].UID, State: controllerprojection.StateConflict, Reason: controllerprojection.ReasonDuplicateBinding},
	}
	return InventoryInput{
		Inventory: inventory, Profiles: profiles, Racks: []*mokkav1alpha1.SGPURack{rackA, rackB}, Nodes: nodes,
		RackResult: controllerack.Result{
			Accepted: true, ResolvedRefs: false,
			ProfileIssues: []controllerack.ProfileIssue{{RackGroup: "missing", ProfileName: "missing", Reason: "NotFound"}},
			Allocation:    allocation,
		},
		Projection: projection,
	}
}

func placement(values ...string) *mokkav1alpha1.RackPlacement {
	return &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key: "pool", Operator: metav1.LabelSelectorOpIn, Values: values,
	}}}}
}

func profile(name string, nodesPerRack, gpus int32) *mokkav1alpha1.SGPUProfile {
	return &mokkav1alpha1.SGPUProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid"), Generation: 1},
		Spec: mokkav1alpha1.SGPUProfileSpec{
			Rack: mokkav1alpha1.SGPUProfileRack{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUNode{GPUs: mokkav1alpha1.SGPUGPUs{Count: gpus}},
		},
	}
}

func node(name string, uid types.UID, eligible bool, pool string) *corev1.Node {
	labels := map[string]string{"pool": pool}
	if eligible {
		labels[allocate.EligibleNodeLabel] = "true"
	}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid, Labels: labels}}
}

type nodeBinding struct {
	name string
	uid  types.UID
}

func statusRack(inventory *mokkav1alpha1.SGPUInventory, name string, uid types.UID, group string, index int32, bindings []nodeBinding) *mokkav1alpha1.SGPURack {
	slots := make([]mokkav1alpha1.SGPURackSlot, len(bindings))
	for i, binding := range bindings {
		slots[i] = mokkav1alpha1.SGPURackSlot{Index: int32(i), NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: binding.name, UID: binding.uid}}
	}
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, UID: uid, Generation: 4,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(inventory, mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"))},
		},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
			ProfileRef:   mokkav1alpha1.SGPURackProfileReference{Name: "p", UID: "p-uid", Revision: "revision"},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: group, RackIndex: index},
			Slots:        slots,
		},
	}
}

func groupKey(inventory *mokkav1alpha1.SGPUInventory, group string) allocate.GroupKey {
	return allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: group}
}

func binding(inventory *mokkav1alpha1.SGPUInventory, group string, rackIndex, slotIndex int32, node *corev1.Node) allocate.Binding {
	return allocate.Binding{
		Coordinate: allocate.Coordinate{Group: groupKey(inventory, group), RackIndex: rackIndex, SlotIndex: slotIndex},
		Node:       allocate.NodeReference{Name: node.Name, UID: node.UID},
	}
}

func allocationNode(node *corev1.Node) allocate.Node {
	return allocate.Node{Name: node.Name, UID: node.UID, Labels: node.Labels}
}

func condition(conditions []metav1.Condition, conditionType string) metav1.Condition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	return metav1.Condition{}
}

type fakeInventoryWriter struct {
	object       *mokkav1alpha1.SGPUInventory
	conflictOnce bool
	updates      int
}

func (f *fakeInventoryWriter) Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPUInventory, error) {
	return f.object.DeepCopy(), nil
}

func (f *fakeInventoryWriter) UpdateStatus(_ context.Context, candidate *mokkav1alpha1.SGPUInventory, _ metav1.UpdateOptions) (*mokkav1alpha1.SGPUInventory, error) {
	f.updates++
	if f.conflictOnce {
		f.conflictOnce = false
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "sgpuinventories"}, candidate.Name, errors.New("test conflict"))
	}
	f.object = candidate.DeepCopy()
	return f.object.DeepCopy(), nil
}

type fakeRackWriter struct {
	object       *mokkav1alpha1.SGPURack
	getErr       error
	updateErr    error
	conflictOnce bool
	updates      int
}

func (f *fakeRackWriter) Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.object.DeepCopy(), nil
}

func (f *fakeRackWriter) UpdateStatus(_ context.Context, candidate *mokkav1alpha1.SGPURack, _ metav1.UpdateOptions) (*mokkav1alpha1.SGPURack, error) {
	f.updates++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.conflictOnce {
		f.conflictOnce = false
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "sgpuracks"}, candidate.Name, errors.New("test conflict"))
	}
	f.object = candidate.DeepCopy()
	return f.object.DeepCopy(), nil
}
