// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	sgpuinventory "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
)

func TestAssignmentsForNodeRequiresReadyCacheAndExactIdentity(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.Indexers())
	rack := testRack(testNode())
	replacement := rack.DeepCopy()
	replacement.Name = "replacement"
	replacement.UID = "replacement-rack-uid"
	replacement.Spec.Nodes[0].NodeRef.UID = "replacement-node-uid"
	require.NoError(t, indexer.Add(rack))
	require.NoError(t, indexer.Add(replacement))
	controller := &Controller{snapshot: &informerCache{racks: indexer}}
	ref := *rack.Spec.Nodes[0].NodeRef

	_, err := controller.AssignmentsForNode(ref)
	require.ErrorIs(t, err, ErrCacheNotReady)

	controller.cacheReady.Store(true)
	_, err = controller.AssignmentsForNode(mokkav1alpha1.SGPUNodeReference{Name: ref.Name})
	require.EqualError(t, err, "assignment lookup requires exact Node name and UID")

	assignments, err := controller.AssignmentsForNode(ref)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, rack.Name, assignments[0].Rack.Name)
	require.Equal(t, ref, *assignments[0].Node.NodeRef)

	assignments, err = controller.AssignmentsForNode(mokkav1alpha1.SGPUNodeReference{
		Name: ref.Name, UID: replacement.Spec.Nodes[0].NodeRef.UID,
	})
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, replacement.Name, assignments[0].Rack.Name)

	assignments, err = controller.AssignmentsForNode(mokkav1alpha1.SGPUNodeReference{
		Name: "missing", UID: ref.UID,
	})
	require.NoError(t, err)
	require.NotNil(t, assignments)
	require.Empty(t, assignments)
}

func TestAssignmentsForNodeReturnsDeterministicCallerOwnedSnapshots(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, sgpuinventory.Indexers())
	ref := mokkav1alpha1.SGPUNodeReference{Name: "node", UID: "node-uid"}
	later := assignmentRack("z-rack", "z-rack-uid", ref, 2)
	earlier := assignmentRack("a-rack", "a-rack-uid", ref, 3)
	earlier.Spec.Nodes = append(earlier.Spec.Nodes, mokkav1alpha1.SGPURackNode{
		Index: 1, NodeRef: ref.DeepCopy(),
	})
	deleting := assignmentRack("m-rack", "m-rack-uid", ref, 0)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{sgpuinventory.RackFinalizer}
	for _, rack := range []*mokkav1alpha1.SGPURack{later, deleting, earlier} {
		require.NoError(t, indexer.Add(rack))
	}
	controller := &Controller{snapshot: &informerCache{racks: indexer}}
	controller.cacheReady.Store(true)

	assignments, err := controller.AssignmentsForNode(ref)
	require.NoError(t, err)
	require.Len(t, assignments, 4)
	require.Equal(t, []string{"a-rack", "a-rack", "m-rack", "z-rack"}, []string{
		assignments[0].Rack.Name, assignments[1].Rack.Name,
		assignments[2].Rack.Name, assignments[3].Rack.Name,
	})
	require.Equal(t, []int32{1, 3}, []int32{assignments[0].Node.Index, assignments[1].Node.Index})
	require.NotNil(t, assignments[2].Rack.DeletionTimestamp)
	for _, assignment := range assignments {
		offset := assignmentNodeOffset(assignment)
		require.NotEqual(t, -1, offset)
		require.Same(t, assignment.Node, &assignment.Rack.Spec.Nodes[offset])
	}

	assignments[0].Rack.Spec.Nodes[0].NodeRef.Name = "mutated"
	require.Equal(t, ref.Name, assignments[1].Rack.Spec.Nodes[0].NodeRef.Name)
	stored, exists, err := indexer.GetByKey(earlier.Name)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, ref.Name, stored.(*mokkav1alpha1.SGPURack).Spec.Nodes[0].NodeRef.Name)
}

func assignmentNodeOffset(assignment AssignmentSnapshot) int {
	for index := range assignment.Rack.Spec.Nodes {
		if assignment.Node == &assignment.Rack.Spec.Nodes[index] {
			return index
		}
	}
	return -1
}

func assignmentRack(name string, uid types.UID, ref mokkav1alpha1.SGPUNodeReference, index int32) *mokkav1alpha1.SGPURack {
	return &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid},
		Spec: mokkav1alpha1.SGPURackSpec{Nodes: []mokkav1alpha1.SGPURackNode{{
			Index: index, NodeRef: ref.DeepCopy(),
		}}},
	}
}
