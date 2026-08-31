// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"fmt"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	controllernodes "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/nodecatalog"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestStatusSnapshotsBoundWorkToOneInventoryAndRack(t *testing.T) {
	const (
		inventoryCount    = 100
		racksPerInventory = 10
		nodesPerRack      = 100
	)

	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	nodes := controllernodes.New()
	profile := &mokkav1alpha1.SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile", UID: "profile-uid"}}
	require.NoError(t, profileIndexer.Add(profile))

	allNodes := make([]*corev1.Node, 0, inventoryCount*racksPerInventory*nodesPerRack)
	var targetInventory *mokkav1alpha1.SGPUInventory
	var targetRack *mokkav1alpha1.SGPURack
	for inventoryIndex := range inventoryCount {
		inventory := scaleStatusInventory(inventoryIndex, racksPerInventory)
		require.NoError(t, inventoryIndexer.Add(inventory))
		if inventoryIndex == 0 {
			targetInventory = inventory
		}
		for rackIndex := range racksPerInventory {
			globalRackIndex := inventoryIndex*racksPerInventory + rackIndex
			rack := scaleStatusRack(inventory, globalRackIndex, rackIndex, nodesPerRack)
			require.NoError(t, rackIndexer.Add(rack))
			if globalRackIndex == 0 {
				targetRack = rack
			}
			for nodeIndex := range nodesPerRack {
				nodeIndex := globalRackIndex*nodesPerRack + nodeIndex
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("node-%d", nodeIndex), UID: types.UID(fmt.Sprintf("node-uid-%d", nodeIndex)),
					Labels: map[string]string{
						allocate.EligibleNodeLabel: "true",
						"inventory":                fmt.Sprintf("inventory-%d", inventoryIndex),
					},
				}}
				nodes.Upsert(node)
				allNodes = append(allNodes, node)
			}
		}
	}

	snapshot := newInformerCache(
		mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
		mokkalisters.NewSGPURackProfileLister(profileIndexer),
		rackIndexer,
		nodes,
		nil,
		DefaultOptions(),
	)
	projection := &recordingScopedProjection{
		inventoryOutcomes: []controllerprojection.Outcome{{
			InventoryName: targetInventory.Name, InventoryUID: targetInventory.UID,
			RackName: targetRack.Name, RackUID: targetRack.UID,
		}},
		rackOutcomes: []controllerprojection.Outcome{{
			InventoryName: targetInventory.Name, InventoryUID: targetInventory.UID,
			RackName: targetRack.Name, RackUID: targetRack.UID,
		}},
	}

	inventoryInput, inventoryStats, err := inventoryStatusInput(snapshot, projection, newResultStore(), targetInventory)
	require.NoError(t, err)
	require.Len(t, inventoryInput.Racks, racksPerInventory)
	require.Len(t, inventoryInput.Nodes, racksPerInventory*nodesPerRack)
	require.Equal(t, racksPerInventory*nodesPerRack, inventoryStats.nodesExamined)
	require.Equal(t, racksPerInventory, inventoryStats.racksExamined)
	require.Equal(t, len(projection.inventoryOutcomes), inventoryStats.outcomesExamined)
	require.Equal(t, []exactObjectReference{{name: targetInventory.Name, uid: targetInventory.UID}}, projection.inventoryCalls)
	require.Equal(t, projection.inventoryOutcomes, inventoryInput.Projection)

	rackInput, rackStats, err := rackStatusInput(snapshot, projection, targetRack)
	require.NoError(t, err)
	require.Len(t, rackInput.Racks, 1)
	require.Len(t, rackInput.Nodes, nodesPerRack)
	require.Equal(t, nodesPerRack, rackStats.nodesExamined)
	require.Equal(t, nodesPerRack, rackStats.relatedRackLookups)
	require.Equal(t, nodesPerRack, rackStats.relatedRacksExamined)
	require.Equal(t, len(projection.rackOutcomes), rackStats.outcomesExamined)
	require.Equal(t, []exactObjectReference{{name: targetRack.Name, uid: targetRack.UID}}, projection.rackCalls)
	require.Equal(t, projection.rackOutcomes, rackInput.Projection)

	foreignOutcome := controllerprojection.Outcome{
		InventoryName: "inventory-99", InventoryUID: "inventory-uid-99",
		RackName: "rack-999", RackUID: "rack-uid-999", NodeIndex: 99,
		NodeName: "node-99999", NodeUID: "node-uid-99999", State: controllerprojection.StateConflict,
		Reason: controllerprojection.ReasonNodeMetadataConflict,
	}
	now := metav1.NewTime(time.Unix(100, 0))
	legacyInventoryInput := inventoryInput
	legacyInventoryInput.Nodes = allNodes
	legacyInventoryInput.Projection = append(slices.Clone(inventoryInput.Projection), foreignOutcome)
	require.Equal(t,
		controllerstatus.ComputeInventory(legacyInventoryInput, now),
		controllerstatus.ComputeInventory(inventoryInput, now),
		"scoping must preserve inventory status computed from the former global inputs",
	)
	legacyRackInput := rackInput
	legacyRackInput.Nodes = allNodes
	legacyRackInput.Projection = append(slices.Clone(rackInput.Projection), foreignOutcome)
	require.Equal(t,
		controllerstatus.ComputeRack(legacyRackInput, now),
		controllerstatus.ComputeRack(rackInput, now),
		"scoping must preserve rack status computed from the former global inputs",
	)
}

func TestStatusNodeCandidatesPreserveSelectorAndBindingSemantics(t *testing.T) {
	catalog := controllernodes.New()
	nodes := []*corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "a", UID: "a-uid", Labels: map[string]string{"pool": "a", "zone": "east"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "b", UID: "b-uid", Labels: map[string]string{"pool": "b", "zone": "west"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "c", UID: "c-uid", Labels: map[string]string{"pool": "c"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled", UID: "unlabelled-uid"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "bound", UID: "bound-uid", Labels: map[string]string{"pool": "foreign"}}},
	}
	for _, node := range nodes {
		catalog.Upsert(node)
	}
	snapshot := &informerCache{nodes: catalog}

	tests := []struct {
		name         string
		selector     string
		bound        map[string]struct{}
		want         []string
		wantExamined int
	}{
		{name: "equality uses value index", selector: "pool=a", bound: map[string]struct{}{"bound": {}}, want: []string{"a", "bound"}, wantExamined: 2},
		{name: "set membership uses value index", selector: "pool in (a,c)", want: []string{"a", "c"}, wantExamined: 2},
		{name: "existence uses key index", selector: "zone", want: []string{"a", "b"}, wantExamined: 2},
		{name: "negative selector examines the possible set", selector: "pool!=a", want: []string{"b", "bound", "c", "unlabelled"}, wantExamined: len(nodes)},
		{name: "absence selector examines the possible set", selector: "!pool", want: []string{"unlabelled"}, wantExamined: len(nodes)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector, err := labels.Parse(test.selector)
			require.NoError(t, err)
			result, err := snapshot.statusNodes([]labels.Selector{selector}, test.bound)
			require.NoError(t, err)
			names := make([]string, 0, len(result.nodes))
			for _, node := range result.nodes {
				names = append(names, node.Name)
			}
			require.Equal(t, test.want, names)
			require.Equal(t, test.wantExamined, result.examined)
		})
	}
}

func TestStatusSnapshotSkipsPlacementForInventoryOutsideRackGroupBudget(t *testing.T) {
	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.Indexers())
	var blocked *mokkav1alpha1.SGPUInventory
	for index := range controllerack.MaxRackGroups + 1 {
		inventory := scaleStatusInventory(index, 1)
		inventory.CreationTimestamp = metav1.NewTime(time.Unix(int64(index+1), 0))
		require.NoError(t, inventoryIndexer.Add(inventory))
		blocked = inventory
	}
	nodes := controllernodes.New()
	nodes.Upsert(&corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "blocked-candidate", UID: "blocked-candidate-uid",
		Labels: map[string]string{
			allocate.EligibleNodeLabel: "true",
			"inventory":                blocked.Name,
		},
	}})
	snapshot := newInformerCache(
		mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
		mokkalisters.NewSGPURackProfileLister(profileIndexer),
		rackIndexer,
		nodes,
		nil,
		DefaultOptions(),
	)

	input, work, err := inventoryStatusInput(snapshot, &recordingScopedProjection{}, newResultStore(), blocked)
	require.NoError(t, err)
	require.Empty(t, input.Nodes)
	require.Zero(t, work.nodesExamined,
		"a rejected declaration must not classify Nodes for status")
}

func scaleStatusInventory(index, rackCount int) *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("inventory-%d", index), UID: types.UID(fmt.Sprintf("inventory-uid-%d", index)),
		},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.RackGroup{{
			ID: "group", Count: int32(rackCount), ProfileRef: mokkav1alpha1.ProfileReference{Name: "profile"},
			Placement: &mokkav1alpha1.RackPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"inventory": fmt.Sprintf("inventory-%d", index)},
			}},
		}}},
	}
}

func scaleStatusRack(inventory *mokkav1alpha1.SGPUInventory, globalIndex, rackIndex, nodesPerRack int) *mokkav1alpha1.SGPURack {
	rack := &mokkav1alpha1.SGPURack{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("rack-%d", globalIndex), UID: types.UID(fmt.Sprintf("rack-uid-%d", globalIndex)),
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
				inventory, mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"),
			)},
		},
		Spec: mokkav1alpha1.SGPURackSpec{
			InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
			ProfileRef:   mokkav1alpha1.SGPURackProfileReference{Name: "profile", UID: "profile-uid"},
			Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: int32(rackIndex)},
			Nodes:        make([]mokkav1alpha1.SGPURackNode, nodesPerRack),
		},
	}
	for rackNodeIndex := range nodesPerRack {
		nodeIndex := globalIndex*nodesPerRack + rackNodeIndex
		rack.Spec.Nodes[rackNodeIndex] = mokkav1alpha1.SGPURackNode{
			Index: int32(rackNodeIndex),
			NodeRef: &mokkav1alpha1.SGPUNodeReference{
				Name: fmt.Sprintf("node-%d", nodeIndex), UID: types.UID(fmt.Sprintf("node-uid-%d", nodeIndex)),
			},
		}
	}
	return rack
}

type exactObjectReference struct {
	name string
	uid  types.UID
}

type recordingScopedProjection struct {
	inventoryOutcomes []controllerprojection.Outcome
	rackOutcomes      []controllerprojection.Outcome
	inventoryCalls    []exactObjectReference
	rackCalls         []exactObjectReference
}

func (r *recordingScopedProjection) OutcomesForInventory(name string, uid types.UID) []controllerprojection.Outcome {
	r.inventoryCalls = append(r.inventoryCalls, exactObjectReference{name: name, uid: uid})
	return r.inventoryOutcomes
}

func (r *recordingScopedProjection) OutcomesForRack(name string, uid types.UID) []controllerprojection.Outcome {
	r.rackCalls = append(r.rackCalls, exactObjectReference{name: name, uid: uid})
	return r.rackOutcomes
}
