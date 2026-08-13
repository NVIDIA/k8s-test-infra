// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"

	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	controllerstatus "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/status"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/mokka/v1alpha1"
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

func reconcileInventoryStatus(
	ctx context.Context,
	snapshot *informerCache,
	reconciler *controllerstatus.Reconciler,
	projection *controllerprojection.Controller,
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
	racks, err := snapshot.RacksByInventoryUID(inventory.UID)
	if err != nil {
		return err
	}
	nodes, err := snapshot.Nodes()
	if err != nil {
		return err
	}
	profiles := make(map[string]*mokkav1alpha1.SGPUProfile, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		profile, getErr := snapshot.Profile(group.ProfileRef.Name)
		if getErr == nil {
			profiles[group.ProfileRef.Name] = profile
		} else if !apierrors.IsNotFound(getErr) {
			return getErr
		}
	}
	_, err = reconciler.ReconcileInventory(ctx, controllerstatus.InventoryInput{
		Inventory:  inventory,
		Profiles:   profiles,
		Racks:      racks,
		Nodes:      nodes,
		RackResult: results.get(inventory.Name, inventory.UID),
		Projection: projection.Outcomes(),
	})
	return err
}

func reconcileRackStatus(
	ctx context.Context,
	snapshot *informerCache,
	reconciler *controllerstatus.Reconciler,
	projection *controllerprojection.Controller,
	key statusKey,
) error {
	rack, err := snapshot.Rack(key.name)
	if apierrors.IsNotFound(err) || (err == nil && rack.UID != key.uid) {
		return nil
	}
	if err != nil {
		return err
	}
	nodes, err := snapshot.Nodes()
	if err != nil {
		return err
	}
	related := map[string]*mokkav1alpha1.SGPURack{rack.Name: rack}
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef == nil {
			continue
		}
		bound, getErr := snapshot.RacksByNodeUID(slot.NodeRef.UID)
		if getErr != nil {
			return getErr
		}
		for _, other := range bound {
			related[other.Name] = other
		}
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(related))
	for _, relatedRack := range related {
		racks = append(racks, relatedRack)
	}
	_, err = reconciler.ReconcileRack(ctx, controllerstatus.RackInput{
		Rack: rack, Racks: racks, Nodes: nodes, Projection: projection.Outcomes(),
	})
	return err
}
