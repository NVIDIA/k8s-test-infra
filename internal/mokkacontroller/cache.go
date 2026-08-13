// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"

	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
)

const (
	statusNodeByLabelKeyIndex   = "mokkaStatusNodeByLabelKey"
	statusNodeByLabelValueIndex = "mokkaStatusNodeByLabelValue"
)

type informerCache struct {
	inventories mokkalisters.SGPUInventoryLister
	profiles    mokkalisters.SGPUProfileLister
	racks       cache.Indexer
	nodes       cache.Indexer
	liveNodes   corev1client.NodeInterface
}

var _ controllerack.Cache = (*informerCache)(nil)
var _ controllerprojection.Cache = (*informerCache)(nil)

func newInformerCache(
	inventories mokkalisters.SGPUInventoryLister,
	profiles mokkalisters.SGPUProfileLister,
	racks cache.Indexer,
	nodes cache.Indexer,
	liveNodes corev1client.NodeInterface,
) *informerCache {
	return &informerCache{
		inventories: inventories, profiles: profiles, racks: racks, nodes: nodes, liveNodes: liveNodes,
	}
}

func (c *informerCache) Inventory(name string) (*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.Get(name)
}

func (c *informerCache) Inventories() ([]*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.List(labels.Everything())
}

func (c *informerCache) Profile(name string) (*mokkav1alpha1.SGPUProfile, error) {
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

func (c *informerCache) Nodes() ([]*corev1.Node, error) {
	objects := c.nodes.List()
	nodes := make([]*corev1.Node, 0, len(objects))
	for _, object := range objects {
		node, ok := object.(*corev1.Node)
		if !ok {
			return nil, fmt.Errorf("Node cache contained %T", object)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

type statusNodeSnapshot struct {
	nodes    []*corev1.Node
	examined int
}

func statusNodeIndexers() cache.Indexers {
	return cache.Indexers{
		statusNodeByLabelKeyIndex:   statusNodeLabelKeys,
		statusNodeByLabelValueIndex: statusNodeLabelValues,
	}
}

func statusNodeLabelKeys(object any) ([]string, error) {
	node, ok := object.(*corev1.Node)
	if !ok {
		return nil, fmt.Errorf("Node label-key index received %T", object)
	}
	keys := make([]string, 0, len(node.Labels))
	for key := range node.Labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, nil
}

func statusNodeLabelValues(object any) ([]string, error) {
	node, ok := object.(*corev1.Node)
	if !ok {
		return nil, fmt.Errorf("Node label-value index received %T", object)
	}
	values := make([]string, 0, len(node.Labels))
	for key, value := range node.Labels {
		values = append(values, statusNodeLabelValueKey(key, value))
	}
	slices.Sort(values)
	return values, nil
}

func statusNodeLabelValueKey(key, value string) string {
	return key + "\x00" + value
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
		for _, slot := range rack.Spec.Slots {
			if slot.NodeRef != nil && slot.NodeRef.Name != "" {
				boundNames[slot.NodeRef.Name] = struct{}{}
			}
		}
	}
	return c.statusNodes(selectors, boundNames)
}

func (c *informerCache) statusNodesForRack(rack *mokkav1alpha1.SGPURack) (statusNodeSnapshot, error) {
	boundNames := make(map[string]struct{}, len(rack.Spec.Slots))
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef != nil && slot.NodeRef.Name != "" {
			boundNames[slot.NodeRef.Name] = struct{}{}
		}
	}
	return c.statusNodes(nil, boundNames)
}

func (c *informerCache) statusNodes(selectors []labels.Selector, boundNames map[string]struct{}) (statusNodeSnapshot, error) {
	candidates := make(map[string]*corev1.Node)
	for _, selector := range selectors {
		objects, err := c.statusNodeCandidates(selector)
		if err != nil {
			return statusNodeSnapshot{}, err
		}
		for _, object := range objects {
			node, ok := object.(*corev1.Node)
			if !ok {
				return statusNodeSnapshot{}, fmt.Errorf("Node cache contained %T", object)
			}
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
		object, exists, err := c.nodes.GetByKey(name)
		if err != nil {
			return statusNodeSnapshot{}, err
		}
		if !exists {
			continue
		}
		node, ok := object.(*corev1.Node)
		if !ok {
			return statusNodeSnapshot{}, fmt.Errorf("Node cache contained %T", object)
		}
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

func (c *informerCache) statusNodeCandidates(selector labels.Selector) ([]any, error) {
	if labels.MatchesNothing(selector) {
		return nil, nil
	}
	requirements, selectable := selector.Requirements()
	if !selectable {
		return nil, nil
	}
	for _, requirement := range requirements {
		switch requirement.Operator() {
		case selection.Equals, selection.DoubleEquals, selection.In:
			return c.nodesByLabelValues(requirement.Key(), requirement.Values().List())
		}
	}
	for _, requirement := range requirements {
		if requirement.Operator() == selection.Exists {
			return c.nodes.ByIndex(statusNodeByLabelKeyIndex, requirement.Key())
		}
	}
	return c.nodes.List(), nil
}

func (c *informerCache) nodesByLabelValues(key string, values []string) ([]any, error) {
	objectsByName := make(map[string]any)
	for _, value := range values {
		objects, err := c.nodes.ByIndex(statusNodeByLabelValueIndex, statusNodeLabelValueKey(key, value))
		if err != nil {
			return nil, err
		}
		for _, object := range objects {
			node, ok := object.(*corev1.Node)
			if !ok {
				return nil, fmt.Errorf("Node cache contained %T", object)
			}
			objectsByName[node.Name] = object
		}
	}
	objects := make([]any, 0, len(objectsByName))
	for _, object := range objectsByName {
		objects = append(objects, object)
	}
	return objects, nil
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
func (c *informerCache) Node(name string) (*corev1.Node, error) {
	object, exists, err := c.nodes.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if exists {
		node, ok := object.(*corev1.Node)
		if !ok {
			return nil, fmt.Errorf("Node cache contained %T", object)
		}
		return node, nil
	}
	return c.liveNodes.Get(context.Background(), name, metav1.GetOptions{})
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
	profiles := make(map[string]*mokkav1alpha1.SGPUProfile, len(inventory.Spec.RackGroups))
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
	for _, slot := range rack.Spec.Slots {
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
