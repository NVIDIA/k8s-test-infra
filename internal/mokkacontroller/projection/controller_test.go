// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package projection

import (
	"context"
	"encoding/json"
	"errors"
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

	controller.Project(context.Background(), rack.Name, 0) // clears the prior cleanup acknowledgement for a live binding
	cache.nodes["node"] = testNode("node", "replacement-uid")
	outcome, err = controller.Cleanup(context.Background(), cleanup)
	require.NoError(t, err)
	require.Equal(t, StateCleaned, outcome.State)
	require.True(t, controller.Ready(cleanup))
	require.Empty(t, patcher.calls)
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
