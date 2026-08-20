// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	controllernodes "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/nodecatalog"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
)

type informerCache struct {
	inventories mokkalisters.SGPUInventoryLister
	profiles    mokkalisters.SGPURackProfileLister
	racks       cache.Indexer
	nodes       *controllernodes.Catalog
	liveNodes   corev1client.NodeInterface
	liveTimeout time.Duration
	liveSlots   chan struct{}
}

var _ controllerack.Cache = (*informerCache)(nil)
var _ controllerprojection.Cache = (*informerCache)(nil)

func newInformerCache(
	inventories mokkalisters.SGPUInventoryLister,
	profiles mokkalisters.SGPURackProfileLister,
	racks cache.Indexer,
	nodes *controllernodes.Catalog,
	liveNodes corev1client.NodeInterface,
	options Options,
) *informerCache {
	return &informerCache{
		inventories: inventories, profiles: profiles, racks: racks, nodes: nodes, liveNodes: liveNodes,
		liveTimeout: options.liveNodeGetTimeout(), liveSlots: make(chan struct{}, max(options.Workers, 1)),
	}
}

func (c *informerCache) Inventory(name string) (*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.Get(name)
}

func (c *informerCache) Inventories() ([]*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.List(labels.Everything())
}

func (c *informerCache) Profile(name string) (*mokkav1alpha1.SGPURackProfile, error) {
	return c.profiles.Get(name)
}

func (c *informerCache) Rack(name string) (*mokkav1alpha1.SGPURack, error) {
	object, exists, err := c.racks.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), name)
	}
	rack, ok := object.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack cache contained %T", object)
	}
	return rack, nil
}

func (c *informerCache) Racks() ([]*mokkav1alpha1.SGPURack, error) {
	return castRacks(c.racks.List())
}

func (c *informerCache) RacksByInventoryUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(controllerack.RackByInventoryUIDIndex, string(uid))
	if err != nil {
		return nil, err
	}
	return castRacks(objects)
}

func (c *informerCache) RacksByInventoryGroup(uid types.UID, group string) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(
		controllerack.RackByInventoryGroupIndex,
		controllerack.InventoryGroupIndexKey(uid, group),
	)
	if err != nil {
		return nil, err
	}
	return castRacks(objects)
}

func (c *informerCache) RacksByNodeUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(controllerack.RackByNodeUIDIndex, string(uid))
	if err != nil {
		return nil, err
	}
	return castRacks(objects)
}

func (c *informerCache) AllocationNodeGeneration() uint64 {
	return c.nodes.Generation()
}

func (c *informerCache) AllocationNodes() ([]allocate.Node, error) {
	return c.nodes.Snapshot().AllocationNodes(), nil
}

type statusNodeSnapshot struct {
	nodes    []*corev1.Node
	examined int
}

func (c *informerCache) statusNodesForInventory(
	inventory *mokkav1alpha1.SGPUInventory,
	racks []*mokkav1alpha1.SGPURack,
) (statusNodeSnapshot, error) {
	selectors := make([]labels.Selector, 0, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		var nodeSelector *metav1.LabelSelector
		if group.Placement != nil {
			nodeSelector = group.Placement.NodeSelector
		}
		selector, err := allocate.CompilePlacementSelector(nodeSelector)
		if err != nil {
			continue
		}
		selectors = append(selectors, selector)
	}
	boundNames := make(map[string]struct{})
	for _, rack := range racks {
		if rack == nil {
			continue
		}
		for _, slot := range rack.Spec.Nodes {
			if slot.NodeRef != nil && slot.NodeRef.Name != "" {
				boundNames[slot.NodeRef.Name] = struct{}{}
			}
		}
	}
	return c.statusNodes(selectors, boundNames)
}

func (c *informerCache) statusNodesForRack(rack *mokkav1alpha1.SGPURack) (statusNodeSnapshot, error) {
	boundNames := make(map[string]struct{}, len(rack.Spec.Nodes))
	for _, slot := range rack.Spec.Nodes {
		if slot.NodeRef != nil && slot.NodeRef.Name != "" {
			boundNames[slot.NodeRef.Name] = struct{}{}
		}
	}
	return c.statusNodes(nil, boundNames)
}

func (c *informerCache) statusNodes(selectors []labels.Selector, boundNames map[string]struct{}) (statusNodeSnapshot, error) {
	candidates := make(map[string]*corev1.Node)
	for _, selector := range selectors {
		for _, record := range c.nodes.Candidates(selector) {
			node := record.Node()
			candidates[node.Name] = node
		}
	}

	examined := make(map[string]struct{}, len(candidates)+len(boundNames))
	nodes := make(map[string]*corev1.Node, len(candidates)+len(boundNames))
	for name, node := range candidates {
		examined[name] = struct{}{}
		for _, selector := range selectors {
			if selector.Matches(labels.Set(node.Labels)) {
				nodes[name] = node
				break
			}
		}
	}
	for name := range boundNames {
		record, exists := c.nodes.GetByName(name)
		if !exists {
			continue
		}
		node := record.Node()
		examined[name] = struct{}{}
		nodes[name] = node
	}

	ordered := make([]*corev1.Node, 0, len(nodes))
	for _, node := range nodes {
		ordered = append(ordered, node)
	}
	slices.SortFunc(ordered, func(a, b *corev1.Node) int {
		return compareNodeIdentity(a, b)
	})
	return statusNodeSnapshot{nodes: ordered, examined: len(examined)}, nil
}

func compareNodeIdentity(a, b *corev1.Node) int {
	if a.Name < b.Name {
		return -1
	}
	if a.Name > b.Name {
		return 1
	}
	if a.UID < b.UID {
		return -1
	}
	if a.UID > b.UID {
		return 1
	}
	return 0
}

// Node falls back to an exact GET only for objects absent from the filtered
// cache. That distinguishes an eligibility-loss delete from object deletion
// while keeping steady-state placement entirely informer-backed.
func (c *informerCache) Node(ctx context.Context, name string) (*corev1.Node, error) {
	record, exists := c.nodes.GetByName(name)
	if exists {
		return record.Node(), nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.liveTimeout)
	defer cancel()
	select {
	case c.liveSlots <- struct{}{}:
	case <-requestCtx.Done():
		return nil, context.Cause(requestCtx)
	}
	type result struct {
		node *corev1.Node
		err  error
	}
	response := make(chan result, 1)
	go func() {
		defer func() { <-c.liveSlots }()
		node, err := c.liveNodes.Get(requestCtx, name, metav1.GetOptions{})
		response <- result{node: node, err: err}
	}()
	select {
	case result := <-response:
		if err := context.Cause(requestCtx); err != nil {
			return nil, err
		}
		return result.node, result.err
	case <-requestCtx.Done():
		return nil, context.Cause(requestCtx)
	}
}

func castRacks(objects []any) ([]*mokkav1alpha1.SGPURack, error) {
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, object := range objects {
		rack, ok := object.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", object)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

type projectionSnapshots interface {
	OutcomesForInventory(string, types.UID) []controllerprojection.Outcome
	OutcomesForRack(string, types.UID) []controllerprojection.Outcome
}

type statusSnapshotWork struct {
	nodesExamined        int
	racksExamined        int
	outcomesExamined     int
	relatedRackLookups   int
	relatedRacksExamined int
}

func inventoryStatusInput(
	snapshot *informerCache,
	projection projectionSnapshots,
	results *resultStore,
	inventory *mokkav1alpha1.SGPUInventory,
) (controllerstatus.InventoryInput, statusSnapshotWork, error) {
	racks, err := snapshot.RacksByInventoryUID(inventory.UID)
	if err != nil {
		return controllerstatus.InventoryInput{}, statusSnapshotWork{}, err
	}
	nodes, err := snapshot.statusNodesForInventory(inventory, racks)
	if err != nil {
		return controllerstatus.InventoryInput{}, statusSnapshotWork{}, err
	}
	profiles := make(map[string]*mokkav1alpha1.SGPURackProfile, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		profile, getErr := snapshot.Profile(group.ProfileRef.Name)
		if getErr == nil {
			profiles[group.ProfileRef.Name] = profile
		} else if !apierrors.IsNotFound(getErr) {
			return controllerstatus.InventoryInput{}, statusSnapshotWork{}, getErr
		}
	}
	outcomes := projection.OutcomesForInventory(inventory.Name, inventory.UID)
	return controllerstatus.InventoryInput{
			Inventory:  inventory,
			Profiles:   profiles,
			Racks:      racks,
			Nodes:      nodes.nodes,
			RackResult: results.get(inventory.Name, inventory.UID),
			Projection: outcomes,
		}, statusSnapshotWork{
			nodesExamined: nodes.examined, racksExamined: len(racks), outcomesExamined: len(outcomes),
		}, nil
}

func rackStatusInput(
	snapshot *informerCache,
	projection projectionSnapshots,
	rack *mokkav1alpha1.SGPURack,
) (controllerstatus.RackInput, statusSnapshotWork, error) {
	related := map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}
	stats := statusSnapshotWork{}
	for _, slot := range rack.Spec.Nodes {
		if slot.NodeRef == nil {
			continue
		}
		stats.relatedRackLookups++
		bound, err := snapshot.RacksByNodeUID(slot.NodeRef.UID)
		if err != nil {
			return controllerstatus.RackInput{}, statusSnapshotWork{}, err
		}
		stats.relatedRacksExamined += len(bound)
		for _, other := range bound {
			related[other.Name] = other
		}
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(related))
	for _, relatedRack := range related {
		racks = append(racks, relatedRack)
	}
	slices.SortFunc(racks, func(a, b *mokkav1alpha1.SGPURack) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	nodes, err := snapshot.statusNodesForRack(rack)
	if err != nil {
		return controllerstatus.RackInput{}, statusSnapshotWork{}, err
	}
	outcomes := projection.OutcomesForRack(rack.Name, rack.UID)
	stats.nodesExamined = nodes.examined
	stats.racksExamined = len(racks)
	stats.outcomesExamined = len(outcomes)
	return controllerstatus.RackInput{
		Rack: rack, Racks: racks, Nodes: nodes.nodes, Projection: outcomes,
	}, stats, nil
}

func reconcileInventoryStatus(
	ctx context.Context,
	snapshot *informerCache,
	reconciler *controllerstatus.Reconciler,
	projection projectionSnapshots,
	results *resultStore,
	key statusKey,
) error {
	inventory, err := snapshot.Inventory(key.name)
	if apierrors.IsNotFound(err) || (err == nil && inventory.UID != key.uid) {
		return nil
	}
	if err != nil {
		return err
	}
	input, _, err := inventoryStatusInput(snapshot, projection, results, inventory)
	if err != nil {
		return err
	}
	_, err = reconciler.ReconcileInventory(ctx, input)
	return err
}

func reconcileRackStatus(
	ctx context.Context,
	snapshot *informerCache,
	reconciler *controllerstatus.Reconciler,
	projection projectionSnapshots,
	key statusKey,
) error {
	rack, err := snapshot.Rack(key.name)
	if apierrors.IsNotFound(err) || (err == nil && rack.UID != key.uid) {
		return nil
	}
	if err != nil {
		return err
	}
	input, _, err := rackStatusInput(snapshot, projection, rack)
	if err != nil {
		return err
	}
	_, err = reconciler.ReconcileRack(ctx, input)
	return err
}
