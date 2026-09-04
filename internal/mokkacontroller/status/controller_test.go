// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
)

func TestComputeInventoryStatusExactAggregateMathAndConditions(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := aggregateInput(t)
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
	input := aggregateInput(t)
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

func TestComputeInventoryCapacityBoundaryAndRejectedOverflow(t *testing.T) {
	t.Run("maximum GPUs at supported node boundary", func(t *testing.T) {
		inventory := &mokkav1alpha1.SGPUInventory{
			ObjectMeta: metav1.ObjectMeta{Name: "inventory", UID: "inventory-uid", Generation: 1},
			Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
				ID: "group", Count: 100_000, ProfileRef: mokkav1alpha1.ProfileReference{Name: "p"},
			}}},
		}
		got := ComputeInventory(InventoryInput{
			Inventory: inventory,
			Profiles:  map[string]*mokkav1alpha1.SGPURackProfile{"p": profile("p", 1, 64)},
			RackResult: controllerack.Result{
				Accepted: true, ResolvedRefs: true,
			},
		}, metav1.Now())

		require.Equal(t, mokkav1alpha1.InventoryCapacity{Racks: 100_000, Nodes: 100_000, GPUs: 6_400_000}, got.Capacity)
		require.Equal(t, got.Capacity, got.RackGroups[0].Capacity)
	})

	t.Run("overflowing declaration is rejected without publishing wrapped capacity", func(t *testing.T) {
		inventory := &mokkav1alpha1.SGPUInventory{
			ObjectMeta: metav1.ObjectMeta{Name: "inventory", UID: "inventory-uid", Generation: 1},
			Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
				ID: "group", Count: math.MaxInt32, ProfileRef: mokkav1alpha1.ProfileReference{Name: "p"},
			}}},
		}
		got := ComputeInventory(InventoryInput{
			Inventory: inventory,
			Profiles:  map[string]*mokkav1alpha1.SGPURackProfile{"p": profile("p", math.MaxInt32, math.MaxInt32)},
			RackResult: controllerack.Result{
				Accepted: false, ResolvedRefs: true,
				ValidationReason: controllerack.ReasonCapacityExceeded,
				ValidationError:  "declared capacity cannot be represented",
			},
		}, metav1.Now())

		require.Zero(t, got.Capacity)
		require.Zero(t, got.RackGroups[0].Capacity)
		accepted := condition(got.Conditions, mokkav1alpha1.InventoryConditionAccepted)
		require.Equal(t, metav1.ConditionFalse, accepted.Status)
		require.Equal(t, ReasonCapacityExceeded, accepted.Reason)
		require.Equal(t, ReasonCapacityExceeded,
			condition(got.Conditions, mokkav1alpha1.InventoryConditionProgrammed).Reason)
	})
}

func TestComputeRackStatusCountsExactProjectionAndDuplicateBindings(t *testing.T) {
	input := aggregateInput(t)
	rack := input.Racks[0]
	oldTime := metav1.NewTime(time.Unix(50, 0))
	rack.Status.Conditions = []metav1.Condition{{
		Type: mokkav1alpha1.RackConditionReady, Status: metav1.ConditionFalse,
		Reason: ReasonDuplicateBindings, LastTransitionTime: oldTime,
	}}

	got := ComputeRack(RackInput{Rack: rack, Racks: input.Racks, Nodes: input.Nodes, Projection: input.Projection}, metav1.NewTime(time.Unix(100, 0)))
	require.Equal(t, int32(3), got.AssignedNodes)
	require.Equal(t, metav1.ConditionFalse, condition(got.Conditions, mokkav1alpha1.RackConditionReady).Status)
	require.Equal(t, ReasonDuplicateBindings, condition(got.Conditions, mokkav1alpha1.RackConditionReady).Reason)
	require.Equal(t, oldTime, condition(got.Conditions, mokkav1alpha1.RackConditionReady).LastTransitionTime)
	require.Len(t, got.Conditions, 1)

	clean := rack.DeepCopy()
	clean.Spec.Nodes = clean.Spec.Nodes[:1]
	clean.Status = mokkav1alpha1.SGPURackStatus{}
	got = ComputeRack(RackInput{Rack: clean, Racks: []*mokkav1alpha1.SGPURack{clean}, Nodes: input.Nodes, Projection: input.Projection}, metav1.Now())
	require.Equal(t, metav1.ConditionTrue, condition(got.Conditions, mokkav1alpha1.RackConditionReady).Status)
}

func TestComputeRackDerivesProjectionFromExactCachedNodeMetadata(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*RackInput)
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name: "exact projection without process outcome",
			mutate: func(input *RackInput) {
				input.Projection = nil
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonReady,
		},
		{
			name: "exact projection supersedes stale process error",
			mutate: func(input *RackInput) {
				input.Projection[0].State = controllerprojection.StateConflict
				input.Projection[0].Reason = controllerprojection.ReasonNodeMetadataConflict
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonReady,
		},
		{
			name: "successful process outcome cannot hide missing label",
			mutate: func(input *RackInput) {
				delete(input.Nodes[0].Labels, controllerprojection.AssignedLabel)
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "missing assignment",
			mutate: func(input *RackInput) {
				delete(input.Nodes[0].Annotations, controllerprojection.AssignmentAnnotation)
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "wrong assignment Node UID",
			mutate: func(input *RackInput) {
				assignment := decodeStatusAssignment(input.Nodes[0])
				assignment.NodeUID = "replacement-uid"
				input.Nodes[0].Annotations[controllerprojection.AssignmentAnnotation] = encodeStatusAssignment(assignment)
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "wrong assignment rack UID",
			mutate: func(input *RackInput) {
				assignment := decodeStatusAssignment(input.Nodes[0])
				assignment.Rack.UID = "replacement-rack-uid"
				input.Nodes[0].Annotations[controllerprojection.AssignmentAnnotation] = encodeStatusAssignment(assignment)
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "wrong assignment profile revision",
			mutate: func(input *RackInput) {
				assignment := decodeStatusAssignment(input.Nodes[0])
				assignment.Profile.Revision = "stale-revision"
				input.Nodes[0].Annotations[controllerprojection.AssignmentAnnotation] = encodeStatusAssignment(assignment)
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "same-name Node recreation",
			mutate: func(input *RackInput) {
				input.Nodes[0].UID = "replacement-node-uid"
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonInvalidBindings,
		},
		{
			name: "missing Mokka ownership",
			mutate: func(input *RackInput) {
				input.Nodes[0].ManagedFields = nil
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "foreign co-owner",
			mutate: func(input *RackInput) {
				setStatusManagedFields(input.Nodes[0], "foreign-controller",
					[]string{controllerprojection.AssignedLabel},
					[]string{controllerprojection.AssignmentAnnotation})
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "clique exact",
			mutate: func(input *RackInput) {
				input.Rack.Spec.Identity.FabricUUID = "fabric"
				input.Rack.Spec.Identity.CliqueID = 7
				setStatusProjection(input.Nodes[0], input.Rack, &input.Rack.Spec.Nodes[0])
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonReady,
		},
		{
			name: "clique missing",
			mutate: func(input *RackInput) {
				input.Rack.Spec.Identity.FabricUUID = "fabric"
				input.Rack.Spec.Identity.CliqueID = 7
				setStatusProjection(input.Nodes[0], input.Rack, &input.Rack.Spec.Nodes[0])
				delete(input.Nodes[0].Labels, controllerprojection.CliqueLabel)
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "wrong clique retained",
			mutate: func(input *RackInput) {
				input.Nodes[0].Labels[controllerprojection.CliqueLabel] = "foreign-clique"
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
		{
			name: "empty clique retained",
			mutate: func(input *RackInput) {
				input.Nodes[0].Labels[controllerprojection.CliqueLabel] = ""
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: ReasonProjectionIncomplete,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := singleProjectedRackInput(t)
			test.mutate(&input)

			got := ComputeRack(input, metav1.Now())
			ready := condition(got.Conditions, mokkav1alpha1.RackConditionReady)
			require.Equal(t, test.wantStatus, ready.Status)
			require.Equal(t, test.wantReason, ready.Reason)
		})
	}
}

func TestRestartStatusBeforeProjectionDoesNotFlapAndLaterRepairConverges(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := singleProjectedInventoryInput(t)
	rackInput := RackInput{
		Rack: input.Racks[0], Racks: input.Racks, Nodes: input.Nodes, Projection: input.Projection,
	}

	input.Inventory.Status = ComputeInventory(input, now)
	rackInput.Rack.Status = ComputeRack(rackInput, now)
	input.Projection = nil
	rackInput.Projection = nil
	inventoryWriter := &fakeInventoryWriter{object: input.Inventory.DeepCopy()}
	rackWriter := &fakeRackWriter{object: rackInput.Rack.DeepCopy()}
	reconciler := NewReconciler(inventoryWriter, rackWriter, func() metav1.Time { return now })

	changed, err := reconciler.ReconcileInventory(context.Background(), input)
	require.NoError(t, err)
	require.False(t, changed, "restart status must derive already-correct projection without a local outcome")
	changed, err = reconciler.ReconcileRack(context.Background(), rackInput)
	require.NoError(t, err)
	require.False(t, changed, "status running before projection must preserve Ready")
	require.Zero(t, inventoryWriter.updates)
	require.Zero(t, rackWriter.updates)

	input.Projection = projectedOutcomeFor(input.Racks[0], &input.Racks[0].Spec.Nodes[0])
	rackInput.Projection = input.Projection
	changed, err = reconciler.ReconcileInventory(context.Background(), input)
	require.NoError(t, err)
	require.False(t, changed, "the later projection fast path must not rewrite unchanged status")
	changed, err = reconciler.ReconcileRack(context.Background(), rackInput)
	require.NoError(t, err)
	require.False(t, changed)
	require.Zero(t, inventoryWriter.updates)
	require.Zero(t, rackWriter.updates)

	delete(input.Nodes[0].Labels, controllerprojection.AssignedLabel)
	changed, err = reconciler.ReconcileInventory(context.Background(), input)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = reconciler.ReconcileRack(context.Background(), rackInput)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, ReasonProjectionIncomplete,
		condition(inventoryWriter.object.Status.Conditions, mokkav1alpha1.InventoryConditionProgrammed).Reason)
	require.Equal(t, ReasonProjectionIncomplete,
		condition(rackWriter.object.Status.Conditions, mokkav1alpha1.RackConditionReady).Reason)

	setStatusProjection(input.Nodes[0], input.Racks[0], &input.Racks[0].Spec.Nodes[0])
	input.Projection[0].State = controllerprojection.StateConflict
	input.Projection[0].Reason = controllerprojection.ReasonNodeMetadataConflict
	changed, err = reconciler.ReconcileInventory(context.Background(), input)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = reconciler.ReconcileRack(context.Background(), rackInput)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, metav1.ConditionTrue,
		condition(inventoryWriter.object.Status.Conditions, mokkav1alpha1.InventoryConditionProgrammed).Status)
	require.Equal(t, metav1.ConditionTrue,
		condition(rackWriter.object.Status.Conditions, mokkav1alpha1.RackConditionReady).Status)
	require.Equal(t, 2, inventoryWriter.updates, "only genuine damage and repair may write inventory status")
	require.Equal(t, 2, rackWriter.updates, "only genuine damage and repair may write rack status")
}

func TestConvergedRackStatusEventsAvoidLiveRequestsAtScale(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := singleProjectedRackInput(t)
	input.Rack.Status = ComputeRack(input, now)
	writer := &fakeRackWriter{getErr: errors.New("converged cached status must not issue a live GET")}
	reconciler := NewReconciler(nil, writer, func() metav1.Time { return now })

	for range 100_000 {
		changed, err := reconciler.ReconcileRack(context.Background(), input)
		require.NoError(t, err)
		require.False(t, changed)
	}
	require.Zero(t, writer.gets)
	require.Zero(t, writer.updates)
}

func TestRackStatusRechecksLiveObjectWhenInformerCacheIsStale(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := singleProjectedRackInput(t)
	live := input.Rack.DeepCopy()
	live.Status = ComputeRack(input, now)
	writer := &fakeRackWriter{object: live}

	changed, err := NewReconciler(nil, writer, func() metav1.Time { return now }).
		ReconcileRack(context.Background(), input)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 1, writer.gets)
	require.Zero(t, writer.updates, "stale informer status must not cause a redundant write")
}

func TestRackStatusConflictRechecksLiveStatusBeforeRetryingWrite(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := singleProjectedRackInput(t)
	writer := &fakeRackWriter{object: input.Rack.DeepCopy(), conflictOnce: true, conflictConverges: true}

	changed, err := NewReconciler(nil, writer, func() metav1.Time { return now }).
		ReconcileRack(context.Background(), input)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 2, writer.gets)
	require.Equal(t, 1, writer.updates, "a conflict resolved by another writer must not be written again")
}

func TestRackStatusTrustsCacheAfterInformerObservesPendingWrite(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := singleProjectedRackInput(t)
	writer := &fakeRackWriter{object: input.Rack.DeepCopy()}
	reconciler := NewReconciler(nil, writer, func() metav1.Time { return now })

	changed, err := reconciler.ReconcileRack(context.Background(), input)
	require.NoError(t, err)
	require.True(t, changed)
	cached := input.Rack.DeepCopy()
	cached.Status = writer.object.Status
	input.Rack = cached
	input.Racks[0] = cached
	reconciler.ObserveRackStatus(cached)
	gets := writer.gets
	writer.getErr = errors.New("observed cached status must not issue a live GET")

	changed, err = reconciler.ReconcileRack(context.Background(), input)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, gets, writer.gets)
}

func TestStatusWritersSuppressIdenticalUpdatesAndRetryConflicts(t *testing.T) {
	now := metav1.NewTime(time.Unix(200, 0))
	input := aggregateInput(t)
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
	input := aggregateInput(t)
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
	input := aggregateInput(t)
	input.RackResult.ProfileIssues = nil
	input.RackResult.ResolvedRefs = true
	input.Inventory.Spec.RackGroups = input.Inventory.Spec.RackGroups[:2]
	input.Inventory.Spec.RackGroups[0].Count = 1
	input.Projection[0].State = controllerprojection.StateConflict
	input.Projection[0].Reason = controllerprojection.ReasonNodeMetadataConflict
	input.Projection[0].Message = "owned elsewhere"
	delete(input.Nodes[1].Labels, controllerprojection.AssignedLabel)
	got := ComputeInventory(input, metav1.Now())
	programmed := condition(got.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, ReasonNodeMetadataConflict, programmed.Reason)
}

func TestInvalidInventoryStatusSurfacesStableValidationError(t *testing.T) {
	input := aggregateInput(t)
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

func TestRackAPIInvalidProfileIssueSurfacesInvalidProfileStatus(t *testing.T) {
	input := aggregateInput(t)
	input.RackResult.ResolvedRefs = false
	input.RackResult.ProfileIssues = []controllerack.ProfileIssue{{
		RackGroup: "a", ProfileName: "pa",
		Reason: `rack "inventory-a-0" was rejected by API validation`,
	}}

	got := ComputeInventory(input, metav1.Now())
	resolved := condition(got.Conditions, mokkav1alpha1.InventoryConditionResolvedRefs)
	require.Equal(t, metav1.ConditionFalse, resolved.Status)
	require.Equal(t, ReasonInvalidProfile, resolved.Reason)
	programmed := condition(got.Conditions, mokkav1alpha1.InventoryConditionProgrammed)
	require.Equal(t, metav1.ConditionFalse, programmed.Status)
	require.Equal(t, ReasonReferencesUnresolved, programmed.Reason)
}

func aggregateInput(t testing.TB) InventoryInput {
	t.Helper()
	inventory := &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{Name: "inventory", UID: "inventory-uid", Generation: 7},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{
			{ID: "a", Count: 2, ProfileRef: mokkav1alpha1.ProfileReference{Name: "pa"}, Placement: placement("a", "both")},
			{ID: "b", Count: 1, ProfileRef: mokkav1alpha1.ProfileReference{Name: "pb"}, Placement: placement("b", "both")},
			{ID: "missing", Count: 4, ProfileRef: mokkav1alpha1.ProfileReference{Name: "missing"}, Placement: placement("missing")},
		}},
	}
	profiles := map[string]*mokkav1alpha1.SGPURackProfile{
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
		{RackName: rackA.Name, RackUID: rackA.UID, NodeIndex: 0, NodeName: nodes[1].Name, NodeUID: nodes[1].UID, State: controllerprojection.StateProjected},
		{RackName: rackB.Name, RackUID: rackB.UID, NodeIndex: 0, NodeName: nodes[4].Name, NodeUID: nodes[4].UID, State: controllerprojection.StateProjected},
		{RackName: rackA.Name, RackUID: rackA.UID, NodeIndex: 1, NodeName: nodes[2].Name, NodeUID: nodes[2].UID, State: controllerprojection.StateConflict, Reason: controllerprojection.ReasonDuplicateBinding},
	}
	setStatusProjection(nodes[1], rackA, &rackA.Spec.Nodes[0])
	setStatusProjection(nodes[4], rackB, &rackB.Spec.Nodes[0])
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

func singleProjectedInventoryInput(t testing.TB) InventoryInput {
	t.Helper()
	input := aggregateInput(t)
	input.Inventory.Spec.RackGroups = input.Inventory.Spec.RackGroups[:1]
	input.Inventory.Spec.RackGroups[0].Count = 1
	input.Racks[0].Spec.Nodes = input.Racks[0].Spec.Nodes[:1]
	input.Racks = input.Racks[:1]
	input.Nodes = input.Nodes[1:2]
	input.RackResult = controllerack.Result{Accepted: true, ResolvedRefs: true}
	input.Projection = projectedOutcomeFor(input.Racks[0], &input.Racks[0].Spec.Nodes[0])
	return input
}

func singleProjectedRackInput(t testing.TB) RackInput {
	t.Helper()
	input := singleProjectedInventoryInput(t)
	rack := input.Racks[0].DeepCopy()
	node := input.Nodes[0].DeepCopy()
	setStatusProjection(node, rack, &rack.Spec.Nodes[0])
	return RackInput{
		Rack: rack, Racks: []*mokkav1alpha1.SGPURack{rack}, Nodes: []*corev1.Node{node},
		Projection: projectedOutcomeFor(rack, &rack.Spec.Nodes[0]),
	}
}

func projectedOutcomeFor(
	rack *mokkav1alpha1.SGPURack,
	slot *mokkav1alpha1.SGPURackNode,
) []controllerprojection.Outcome {
	return []controllerprojection.Outcome{{
		InventoryName: rack.Spec.InventoryRef.Name,
		InventoryUID:  rack.Spec.InventoryRef.UID,
		RackGroup:     rack.Spec.Identity.RackGroup,
		RackName:      rack.Name,
		RackUID:       rack.UID,
		RackIndex:     rack.Spec.Identity.RackIndex,
		NodeIndex:     slot.Index,
		NodeName:      slot.NodeRef.Name,
		NodeUID:       slot.NodeRef.UID,
		State:         controllerprojection.StateProjected,
	}}
}

func setStatusProjection(
	node *corev1.Node,
	rack *mokkav1alpha1.SGPURack,
	slot *mokkav1alpha1.SGPURackNode,
) {
	assignment, err := controllerprojection.EncodeAssignment(rack, slot)
	if err != nil {
		panic(err)
	}
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	node.Labels[controllerprojection.AssignedLabel] = "true"
	node.Labels[controllerprojection.CliqueLabel] =
		fmt.Sprintf("%s.%d", rack.Spec.Identity.FabricUUID, rack.Spec.Identity.CliqueID)
	labels := []string{controllerprojection.AssignedLabel, controllerprojection.CliqueLabel}
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[controllerprojection.AssignmentAnnotation] = assignment
	node.ManagedFields = nil
	setStatusManagedFields(node, controllerprojection.FieldManager, labels,
		[]string{controllerprojection.AssignmentAnnotation})
}

func setStatusManagedFields(node *corev1.Node, manager string, labelKeys, annotationKeys []string) {
	metadataFields := make(map[string]any)
	if len(labelKeys) > 0 {
		fields := make(map[string]any, len(labelKeys))
		for _, key := range labelKeys {
			fields["f:"+key] = map[string]any{}
		}
		metadataFields["f:labels"] = fields
	}
	if len(annotationKeys) > 0 {
		fields := make(map[string]any, len(annotationKeys))
		for _, key := range annotationKeys {
			fields["f:"+key] = map[string]any{}
		}
		metadataFields["f:annotations"] = fields
	}
	raw, err := json.Marshal(map[string]any{"f:metadata": metadataFields})
	if err != nil {
		panic(err)
	}
	node.ManagedFields = append(node.ManagedFields, metav1.ManagedFieldsEntry{
		Manager: manager, Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1",
		FieldsType: "FieldsV1", FieldsV1: metav1.NewFieldsV1(string(raw)),
	})
}

func decodeStatusAssignment(node *corev1.Node) controllerprojection.Assignment {
	assignment, err := controllerprojection.DecodeAssignment(
		node.Annotations[controllerprojection.AssignmentAnnotation],
	)
	if err != nil {
		panic(err)
	}
	return assignment
}

func encodeStatusAssignment(assignment controllerprojection.Assignment) string {
	raw, err := json.Marshal(assignment)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func placement(values ...string) *mokkav1alpha1.RackPlacement {
	return &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key: "pool", Operator: metav1.LabelSelectorOpIn, Values: values,
	}}}}
}

func profile(name string, nodesPerRack, gpus int32) *mokkav1alpha1.SGPURackProfile {
	return &mokkav1alpha1.SGPURackProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid"), Generation: 1},
		Spec: mokkav1alpha1.SGPURackProfileSpec{
			Rack: mokkav1alpha1.SGPURackShape{NodesPerRack: nodesPerRack},
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
	slots := make([]mokkav1alpha1.SGPURackNode, len(bindings))
	for i, binding := range bindings {
		slots[i] = mokkav1alpha1.SGPURackNode{Index: int32(i), NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: binding.name, UID: binding.uid}}
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
			Nodes:        slots,
		},
	}
}

func groupKey(inventory *mokkav1alpha1.SGPUInventory, group string) allocate.GroupKey {
	return allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: group}
}

func binding(inventory *mokkav1alpha1.SGPUInventory, group string, rackIndex, nodeIndex int32, node *corev1.Node) allocate.Binding {
	return allocate.Binding{
		Coordinate: allocate.Coordinate{Group: groupKey(inventory, group), RackIndex: rackIndex, NodeIndex: nodeIndex},
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
	object            *mokkav1alpha1.SGPURack
	getErr            error
	updateErr         error
	conflictOnce      bool
	conflictConverges bool
	gets              int
	updates           int
}

func (f *fakeRackWriter) Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error) {
	f.gets++
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
		if f.conflictConverges {
			f.object.Status = candidate.Status
		}
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "sgpuracks"}, candidate.Name, errors.New("test conflict"))
	}
	f.object = candidate.DeepCopy()
	return f.object.DeepCopy(), nil
}
