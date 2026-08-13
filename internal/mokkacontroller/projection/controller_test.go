// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
)

func TestProjectAppliesOnlyOwnedMetadataWithExactAssignment(t *testing.T) {
	rack := testRack(true)
	node := testNode("node", "node-uid")
	cache := &fakeCache{nodes: map[string]*corev1.Node{node.Name: node}, racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}}
	patcher := &recordingPatcher{node: node}
	controller := NewController(cache, patcher)

	outcome, err := controller.Project(context.Background(), rack.Name, 0)
	require.NoError(t, err)
	require.Equal(t, StateProjected, outcome.State)
	require.Len(t, patcher.calls, 1)
	call := patcher.calls[0]
	require.Equal(t, types.ApplyPatchType, call.patchType)
	require.Equal(t, FieldManager, call.options.FieldManager)
	require.NotNil(t, call.options.Force)
	require.False(t, *call.options.Force)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(call.data, &payload))
	require.Equal(t, "v1", payload["apiVersion"])
	require.Equal(t, "Node", payload["kind"])
	metadata := payload["metadata"].(map[string]any)
	require.Equal(t, node.Name, metadata["name"])
	require.Equal(t, map[string]any{
		AssignedLabel: "true",
		CliqueLabel:   rack.Spec.Identity.FabricUUID + ".0",
	}, metadata["labels"])
	annotations := metadata["annotations"].(map[string]any)
	require.Len(t, annotations, 1)

	var assignment Assignment
	require.NoError(t, json.Unmarshal([]byte(annotations[AssignmentAnnotation].(string)), &assignment))
	require.Equal(t, AssignmentVersion, assignment.Version)
	require.Equal(t, ObjectReference{Name: "inventory", UID: "inventory-uid"}, assignment.Inventory)
	require.Equal(t, ObjectReference{Name: rack.Name, UID: rack.UID}, assignment.Rack)
	require.Equal(t, ProfileReference{Name: "profile", UID: "profile-uid", Revision: "revision"}, assignment.Profile)
	require.Equal(t, "group", assignment.RackGroup)
	require.Equal(t, int32(0), assignment.RackIndex)
	require.Equal(t, int32(0), assignment.SlotIndex)
	require.Equal(t, types.UID("node-uid"), assignment.NodeUID)
	require.Equal(t, []Outcome{outcome}, controller.Outcomes())
}

func TestProjectPreservesIncompatibleValuesAndSurfacesPatchConflicts(t *testing.T) {
	rack := testRack(true)
	node := testNode("node", "node-uid")
	node.Labels = map[string]string{AssignedLabel: "foreign"}
	cache := &fakeCache{nodes: map[string]*corev1.Node{node.Name: node}, racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}}
	patcher := &recordingPatcher{node: node}
	controller := NewController(cache, patcher)

	outcome, err := controller.Project(context.Background(), rack.Name, 0)
	require.Error(t, err)
	require.ErrorAs(t, err, new(*MetadataConflictError))
	require.Equal(t, StateConflict, outcome.State)
	require.Equal(t, ReasonNodeMetadataConflict, outcome.Reason)
	require.Empty(t, patcher.calls)
	require.Equal(t, "foreign", node.Labels[AssignedLabel])

	node.Labels = nil
	patcher.err = apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, node.Name, errors.New("owned elsewhere"))
	outcome, err = controller.Project(context.Background(), rack.Name, 0)
	require.Error(t, err)
	require.True(t, apierrors.IsConflict(err))
	require.Equal(t, StateConflict, outcome.State)
	require.Equal(t, ReasonNodeMetadataConflict, outcome.Reason)
	require.Len(t, patcher.calls, 1)
}

func TestProjectRejectsDuplicateBindingsAndExactUIDReplacement(t *testing.T) {
	rack := testRack(false)
	duplicate := rack.DeepCopy()
	duplicate.Name = "other-rack"
	duplicate.UID = "other-rack-uid"
	duplicate.Spec.Identity.RackIndex = 1
	cache := &fakeCache{
		nodes: map[string]*corev1.Node{"node": testNode("node", "node-uid")},
		racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack, duplicate.Name: duplicate},
	}
	patcher := &recordingPatcher{}
	controller := NewController(cache, patcher)

	outcome, err := controller.Project(context.Background(), rack.Name, 0)
	require.Error(t, err)
	require.Equal(t, StateConflict, outcome.State)
	require.Equal(t, ReasonDuplicateBinding, outcome.Reason)
	require.Empty(t, patcher.calls)

	delete(cache.racks, duplicate.Name)
	cache.nodes["node"] = testNode("node", "replacement-uid")
	outcome, err = controller.Project(context.Background(), rack.Name, 0)
	require.NoError(t, err)
	require.Equal(t, StateAbsent, outcome.State)
	require.Empty(t, patcher.calls)
}

func TestCleanupRequiresExactAnnotationAndSupportsPartialProgress(t *testing.T) {
	rack := testRack(true)
	node := testNode("node", "node-uid")
	assignment, err := EncodeAssignment(rack, &rack.Spec.Slots[0])
	require.NoError(t, err)
	node.Annotations = map[string]string{AssignmentAnnotation: assignment}
	node.Labels = map[string]string{AssignedLabel: "true", CliqueLabel: "foreign-clique"}
	cache := &fakeCache{nodes: map[string]*corev1.Node{node.Name: node}, racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}}
	patcher := &recordingPatcher{node: node}
	controller := NewController(cache, patcher)
	cleanup := cleanupFor(rack)

	outcome, err := controller.Cleanup(context.Background(), cleanup)
	require.Error(t, err)
	require.Equal(t, StateConflict, outcome.State)
	require.False(t, controller.Ready(cleanup))
	require.Len(t, patcher.calls, 1)
	var partial map[string]any
	require.NoError(t, json.Unmarshal(patcher.calls[0].data, &partial))
	metadata := partial["metadata"].(map[string]any)
	require.Equal(t, map[string]any{AssignedLabel: nil}, metadata["labels"])
	require.Equal(t, map[string]any{AssignmentAnnotation: assignment}, metadata["annotations"], "the binding identity remains until cleanup can finish")

	node.Labels = map[string]string{CliqueLabel: rack.Spec.Identity.FabricUUID + ".0"}
	patcher.calls = nil
	outcome, err = controller.Cleanup(context.Background(), cleanup)
	require.NoError(t, err)
	require.Equal(t, StateCleaned, outcome.State)
	require.True(t, controller.Ready(cleanup))
	require.Len(t, patcher.calls, 1)
	var complete map[string]any
	require.NoError(t, json.Unmarshal(patcher.calls[0].data, &complete))
	metadata = complete["metadata"].(map[string]any)
	require.Equal(t, map[string]any{CliqueLabel: nil}, metadata["labels"])
	require.Equal(t, map[string]any{AssignmentAnnotation: nil}, metadata["annotations"])
}

func TestCleanupTreatsAbsentExactUIDAsCleanAndPreservesStaleAnnotation(t *testing.T) {
	rack := testRack(false)
	node := testNode("node", "node-uid")
	node.Annotations = map[string]string{AssignmentAnnotation: `{"v":1,"nodeUID":"somebody-else"}`}
	cache := &fakeCache{nodes: map[string]*corev1.Node{node.Name: node}, racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}}
	patcher := &recordingPatcher{node: node}
	controller := NewController(cache, patcher)
	cleanup := cleanupFor(rack)

	outcome, err := controller.Cleanup(context.Background(), cleanup)
	require.NoError(t, err)
	require.Equal(t, StateCleaned, outcome.State)
	require.True(t, controller.Ready(cleanup))
	require.Empty(t, patcher.calls)
	require.Contains(t, node.Annotations, AssignmentAnnotation)

	controller.Project(context.Background(), rack.Name, 0) // a stale apply preserves the cleanup acknowledgement
	cache.nodes["node"] = testNode("node", "replacement-uid")
	outcome, err = controller.Cleanup(context.Background(), cleanup)
	require.NoError(t, err)
	require.Equal(t, StateCleaned, outcome.State)
	require.True(t, controller.Ready(cleanup))
	require.Empty(t, patcher.calls)
}

func TestStaleProjectionApplyDoesNotRecreateMetadataAfterCleanup(t *testing.T) {
	rack := testRack(true)
	node := testNode("node", "node-uid")
	assignment, err := EncodeAssignment(rack, &rack.Spec.Slots[0])
	require.NoError(t, err)
	node.Labels = map[string]string{AssignedLabel: "true", CliqueLabel: rack.Spec.Identity.FabricUUID + ".0"}
	node.Annotations = map[string]string{AssignmentAnnotation: assignment}
	cache := &fakeCache{
		nodes: map[string]*corev1.Node{node.Name: node},
		racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack},
	}
	patcher := &recordingPatcher{node: node}
	controller := NewController(cache, patcher)
	cleanup := cleanupFor(rack)

	_, err = controller.Cleanup(context.Background(), cleanup)
	require.NoError(t, err)
	require.True(t, controller.Ready(cleanup))
	require.Len(t, patcher.calls, 1)

	_, err = controller.Project(context.Background(), rack.Name, 0)
	require.NoError(t, err)
	require.True(t, controller.Ready(cleanup), "a stale apply must preserve the cleanup acknowledgement")
	require.Len(t, patcher.calls, 1, "a stale apply must not recreate metadata after cleanup")
}

func TestProjectionStateIsBoundedByLiveExactBindings(t *testing.T) {
	const churn = 4_000

	rack := testRack(false)
	cache := &fakeCache{nodes: make(map[string]*corev1.Node), racks: map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}}
	controller := NewController(cache, &recordingPatcher{})

	for i := range churn {
		nodeName := fmt.Sprintf("node-%d", i)
		nodeUID := types.UID(fmt.Sprintf("node-uid-%d", i))
		rack.UID = types.UID(fmt.Sprintf("rack-uid-%d", i))
		rack.Spec.Slots[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: nodeName, UID: nodeUID}
		cache.nodes = map[string]*corev1.Node{nodeName: testNode(nodeName, nodeUID)}

		_, err := controller.Project(context.Background(), rack.Name, 0)
		require.NoError(t, err)
	}

	outcomes, cleanups := stateSize(controller)
	require.Equal(t, 1, outcomes)
	require.Zero(t, cleanups)
	snapshot := controller.Outcomes()
	require.Len(t, snapshot, 1, "status must not scan historical rack or Node identities")
	require.Equal(t, types.UID("rack-uid-3999"), snapshot[0].RackUID)
	require.Equal(t, types.UID("node-uid-3999"), snapshot[0].NodeUID)
}

func TestCleanupAcknowledgementsAreExactAndBoundedByCachedBindings(t *testing.T) {
	const pendingCount = 2_000

	cache := &fakeCache{nodes: make(map[string]*corev1.Node), racks: make(map[string]*mokkav1alpha1.SGPURack)}
	controller := NewController(cache, &recordingPatcher{})
	pending := make([]controllerack.CleanupNeeded, 0, pendingCount)
	for i := range pendingCount {
		rack := testRack(false)
		rack.Name = fmt.Sprintf("rack-%d", i)
		rack.UID = types.UID(fmt.Sprintf("rack-uid-%d", i))
		rack.Spec.InventoryRef.UID = types.UID(fmt.Sprintf("inventory-uid-%d", i))
		rack.Spec.Identity.RackIndex = int32(i)
		rack.Spec.Slots[0].NodeRef.Name = fmt.Sprintf("node-%d", i)
		rack.Spec.Slots[0].NodeRef.UID = types.UID(fmt.Sprintf("node-uid-%d", i))
		cache.racks[rack.Name] = rack
		cache.nodes[rack.Spec.Slots[0].NodeRef.Name] = testNode(rack.Spec.Slots[0].NodeRef.Name, rack.Spec.Slots[0].NodeRef.UID)
		needed := cleanupFor(rack)

		_, err := controller.Cleanup(context.Background(), needed)
		require.NoError(t, err)
		pending = append(pending, needed)
	}

	outcomes, cleanups := stateSize(controller)
	require.Zero(t, outcomes, "successful cleanup must remove projection status state")
	require.Equal(t, pendingCount, cleanups)
	for _, needed := range pending {
		require.True(t, controller.Ready(needed))
		delete(cache.racks, needed.RackName)
		_, err := controller.Cleanup(context.Background(), needed)
		require.NoError(t, err)
		require.False(t, controller.Ready(needed), "an absent exact binding makes the acknowledgement obsolete")
	}
	outcomes, cleanups = stateSize(controller)
	require.Zero(t, outcomes)
	require.Zero(t, cleanups)
}

func TestCleanupDoesNotAliasRackRecreationOrRetainAbsentRackAcknowledgement(t *testing.T) {
	oldRack := testRack(false)
	oldNode := testNode("node", "node-uid")
	cache := &fakeCache{
		nodes: map[string]*corev1.Node{oldNode.Name: oldNode},
		racks: map[string]*mokkav1alpha1.SGPURack{oldRack.Name: oldRack},
	}
	controller := NewController(cache, &recordingPatcher{})
	oldCleanup := cleanupFor(oldRack)
	_, err := controller.Cleanup(context.Background(), oldCleanup)
	require.NoError(t, err)
	require.True(t, controller.Ready(oldCleanup))

	recreated := oldRack.DeepCopy()
	recreated.UID = "recreated-rack-uid"
	cache.racks[recreated.Name] = recreated
	recreatedCleanup := cleanupFor(recreated)
	require.False(t, controller.Ready(recreatedCleanup), "a recreated rack must not consume the old rack's acknowledgement")

	_, err = controller.Project(context.Background(), recreated.Name, 0)
	require.NoError(t, err)
	_, cleanups := stateSize(controller)
	require.Zero(t, cleanups, "a new exact binding makes acknowledgements for the prior slot identity obsolete")

	delete(cache.racks, recreated.Name)
	_, err = controller.Cleanup(context.Background(), recreatedCleanup)
	require.NoError(t, err)
	require.False(t, controller.Ready(recreatedCleanup))
	outcomes, cleanups := stateSize(controller)
	require.Zero(t, outcomes)
	require.Zero(t, cleanups, "an absent rack has no reconciliation left to consume an acknowledgement")
}

func TestProjectionStateConcurrentAccess(t *testing.T) {
	rack := testRack(false)
	controller := NewController(&fakeCache{}, &recordingPatcher{})
	needed := cleanupFor(rack)
	outcome := outcomeFor(rack, &rack.Spec.Slots[0])

	var workers sync.WaitGroup
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			controller.record(outcome)
			_ = controller.Outcomes()
			controller.completeCleanup(needed, outcome, ReasonCleaned, true)
			_ = controller.Ready(needed)
			controller.beginProjection(rack, &rack.Spec.Slots[0])
		}()
	}
	workers.Wait()

	controller.beginProjection(rack, &rack.Spec.Slots[0])
	controller.record(outcome)
	outcomes, cleanups := stateSize(controller)
	require.Equal(t, 1, outcomes)
	require.Zero(t, cleanups)
}

type fakeCache struct {
	nodes map[string]*corev1.Node
	racks map[string]*mokkav1alpha1.SGPURack
}

func (f *fakeCache) Node(name string) (*corev1.Node, error) {
	node := f.nodes[name]
	if node == nil {
		return nil, apierrors.NewNotFound(corev1.Resource("nodes"), name)
	}
	return node, nil
}

func (f *fakeCache) Rack(name string) (*mokkav1alpha1.SGPURack, error) {
	rack := f.racks[name]
	if rack == nil {
		return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), name)
	}
	return rack, nil
}

func (f *fakeCache) RacksByNodeUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error) {
	var racks []*mokkav1alpha1.SGPURack
	for _, rack := range f.racks {
		for _, slot := range rack.Spec.Slots {
			if slot.NodeRef != nil && slot.NodeRef.UID == uid {
				racks = append(racks, rack)
				break
			}
		}
	}
	return racks, nil
}

type patchCall struct {
	name      string
	patchType types.PatchType
	data      []byte
	options   metav1.PatchOptions
}

type recordingPatcher struct {
	node  *corev1.Node
	err   error
	calls []patchCall
}

func (p *recordingPatcher) Patch(
	_ context.Context,
	name string,
	patchType types.PatchType,
	data []byte,
	options metav1.PatchOptions,
	_ ...string,
) (*corev1.Node, error) {
	p.calls = append(p.calls, patchCall{name: name, patchType: patchType, data: append([]byte(nil), data...), options: options})
	if p.err != nil {
		return nil, p.err
	}
	return p.node, nil
}

func testRack(withFabric bool) *mokkav1alpha1.SGPURack {
	rack := &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-uid", Generation: 2},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: "inventory", UID: "inventory-uid"},
			ProfileRef:   mokkav1alpha1.SGPURackProfileReference{Name: "profile", UID: "profile-uid", Generation: 3, Revision: "revision"},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 0, FabricUUID: "fab", CliqueID: 0},
			Slots:        []mokkav1alpha1.SGPURackSlot{{Index: 0, NodeRef: &mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}}},
		},
	}
	if withFabric {
		rack.Spec.GPUFabric = &mokkav1alpha1.SGPUGPUFabric{}
	}
	return rack
}

func testNode(name string, uid types.UID) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid}}
}

func cleanupFor(rack *mokkav1alpha1.SGPURack) controllerack.CleanupNeeded {
	slot := rack.Spec.Slots[0]
	return controllerack.CleanupNeeded{
		RackName: rack.Name,
		RackUID:  rack.UID,
		Binding: allocate.Binding{
			Coordinate: allocate.Coordinate{
				Group:     allocate.GroupKey{InventoryName: rack.Spec.InventoryRef.Name, InventoryUID: rack.Spec.InventoryRef.UID, RackGroup: rack.Spec.Identity.RackGroup},
				RackIndex: rack.Spec.Identity.RackIndex,
				SlotIndex: slot.Index,
			},
			Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
		},
	}
}

func stateSize(controller *Controller) (outcomes, cleanups int) {
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	return len(controller.outcomes), len(controller.cleaned)
}
