// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

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

	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/stretchr/testify/require"
)

func TestStatusSnapshotsBoundWorkToOneInventoryAndRack(t *testing.T) {
	const (
		inventoryCount    = 100
		racksPerInventory = 10
		slotsPerRack      = 100
	)

	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.InventoryIndexers())
	profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, controllerack.RackIndexers())
	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, statusNodeIndexers())
	profile := &mokkav1alpha1.SGPUProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile", UID: "profile-uid"}}
	require.NoError(t, profileIndexer.Add(profile))

	allNodes := make([]*corev1.Node, 0, inventoryCount*racksPerInventory*slotsPerRack)
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
			rack := scaleStatusRack(inventory, globalRackIndex, rackIndex, slotsPerRack)
			require.NoError(t, rackIndexer.Add(rack))
			if globalRackIndex == 0 {
				targetRack = rack
			}
			for slotIndex := range slotsPerRack {
				nodeIndex := globalRackIndex*slotsPerRack + slotIndex
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("node-%d", nodeIndex), UID: types.UID(fmt.Sprintf("node-uid-%d", nodeIndex)),
					Labels: map[string]string{
						allocate.EligibleNodeLabel: "true",
						"inventory":                fmt.Sprintf("inventory-%d", inventoryIndex),
					},
				}}
				require.NoError(t, nodeIndexer.Add(node))
				allNodes = append(allNodes, node)
			}
		}
	}

	snapshot := newInformerCache(
		mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
		mokkalisters.NewSGPUProfileLister(profileIndexer),
		rackIndexer,
		nodeIndexer,
		nil,
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
	require.Len(t, inventoryInput.Nodes, racksPerInventory*slotsPerRack)
	require.Equal(t, racksPerInventory*slotsPerRack, inventoryStats.nodesExamined)
	require.Equal(t, racksPerInventory, inventoryStats.racksExamined)
	require.Equal(t, len(projection.inventoryOutcomes), inventoryStats.outcomesExamined)
	require.Equal(t, []exactObjectReference{{name: targetInventory.Name, uid: targetInventory.UID}}, projection.inventoryCalls)
	require.Equal(t, projection.inventoryOutcomes, inventoryInput.Projection)

	rackInput, rackStats, err := rackStatusInput(snapshot, projection, targetRack)
	require.NoError(t, err)
	require.Len(t, rackInput.Racks, 1)
	require.Len(t, rackInput.Nodes, slotsPerRack)
	require.Equal(t, slotsPerRack, rackStats.nodesExamined)
	require.Equal(t, slotsPerRack, rackStats.relatedRackLookups)
	require.Equal(t, slotsPerRack, rackStats.relatedRacksExamined)
	require.Equal(t, len(projection.rackOutcomes), rackStats.outcomesExamined)
	require.Equal(t, []exactObjectReference{{name: targetRack.Name, uid: targetRack.UID}}, projection.rackCalls)
	require.Equal(t, projection.rackOutcomes, rackInput.Projection)

	foreignOutcome := controllerprojection.Outcome{
		InventoryName: "inventory-99", InventoryUID: "inventory-uid-99",
		RackName: "rack-999", RackUID: "rack-uid-999", SlotIndex: 99,
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
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, statusNodeIndexers())
	nodes := []*corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "a", Labels: map[string]string{"pool": "a", "zone": "east"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "b", Labels: map[string]string{"pool": "b", "zone": "west"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"pool": "c"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "bound", Labels: map[string]string{"pool": "foreign"}}},
	}
	for _, node := range nodes {
		require.NoError(t, indexer.Add(node))
	}
	snapshot := &informerCache{nodes: indexer}

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

func scaleStatusInventory(index, rackCount int) *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("inventory-%d", index), UID: types.UID(fmt.Sprintf("inventory-uid-%d", index)),
		},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.SGPURackGroup{{
			ID: "group", Count: int32(rackCount), ProfileRef: corev1.LocalObjectReference{Name: "profile"},
			Placement: &mokkav1alpha1.SGPUPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"inventory": fmt.Sprintf("inventory-%d", index)},
			}},
		}}},
	}
}

func scaleStatusRack(inventory *mokkav1alpha1.SGPUInventory, globalIndex, rackIndex, slots int) *mokkav1alpha1.SGPURack {
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
			Slots:        make([]mokkav1alpha1.SGPURackSlot, slots),
		},
	}
	for slotIndex := range slots {
		nodeIndex := globalIndex*slots + slotIndex
		rack.Spec.Slots[slotIndex] = mokkav1alpha1.SGPURackSlot{
			Index: int32(slotIndex),
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
