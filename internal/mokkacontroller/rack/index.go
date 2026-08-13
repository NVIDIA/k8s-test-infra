// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package rack

import (
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/listers"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/mokka/v1alpha1"
)

const (
	InventoryByProfileNameIndex = "mokkaInventoryByProfileName"
	RackByInventoryUIDIndex     = "mokkaRackByInventoryUID"
	RackByInventoryGroupIndex   = "mokkaRackByInventoryGroup"
	RackByNodeUIDIndex          = "mokkaRackByNodeUID"
	RackByNodeNameIndex         = "mokkaRackByNodeName"
)

// InventoryIndexers provides the dependency index needed by profile events.
func InventoryIndexers() cache.Indexers {
	return cache.Indexers{InventoryByProfileNameIndex: inventoryByProfileName}
}

// RackIndexers provides all rack lookups used by reconciliation and later
// projection event routing.
func RackIndexers() cache.Indexers {
	return cache.Indexers{
		RackByInventoryUIDIndex:   rackByInventoryUID,
		RackByInventoryGroupIndex: rackByInventoryGroup,
		RackByNodeUIDIndex:        rackByNodeUID,
		RackByNodeNameIndex:       rackByNodeName,
	}
}

// InventoryGroupIndexKey returns the rack index key for an exact inventory
// instance and group.
func InventoryGroupIndexKey(inventoryUID types.UID, group string) string {
	return string(inventoryUID) + "\x00" + group
}

func inventoryByProfileName(obj any) ([]string, error) {
	inventory, ok := obj.(*mokkav1alpha1.SGPUInventory)
	if !ok {
		return nil, fmt.Errorf("inventory index received %T", obj)
	}
	values := make([]string, 0, len(inventory.Spec.RackGroups))
	seen := make(map[string]struct{}, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		if group.ProfileRef.Name == "" {
			continue
		}
		if _, exists := seen[group.ProfileRef.Name]; exists {
			continue
		}
		seen[group.ProfileRef.Name] = struct{}{}
		values = append(values, group.ProfileRef.Name)
	}
	slices.Sort(values)
	return values, nil
}

func rackByInventoryUID(obj any) ([]string, error) {
	rack, ok := obj.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack index received %T", obj)
	}
	values := make([]string, 0, 2)
	if rack.Spec.InventoryRef.UID != "" {
		values = append(values, string(rack.Spec.InventoryRef.UID))
	}
	if owner := controllerInventoryOwner(rack); owner != nil && owner.UID != "" &&
		!slices.Contains(values, string(owner.UID)) {
		values = append(values, string(owner.UID))
	}
	return values, nil
}

func rackByInventoryGroup(obj any) ([]string, error) {
	rack, ok := obj.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack index received %T", obj)
	}
	if rack.Spec.InventoryRef.UID == "" || rack.Spec.Identity.RackGroup == "" {
		return nil, nil
	}
	return []string{InventoryGroupIndexKey(rack.Spec.InventoryRef.UID, rack.Spec.Identity.RackGroup)}, nil
}

func rackByNodeUID(obj any) ([]string, error) {
	rack, ok := obj.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack index received %T", obj)
	}
	values := make([]string, 0)
	seen := make(map[types.UID]struct{})
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef == nil || slot.NodeRef.UID == "" {
			continue
		}
		if _, exists := seen[slot.NodeRef.UID]; exists {
			continue
		}
		seen[slot.NodeRef.UID] = struct{}{}
		values = append(values, string(slot.NodeRef.UID))
	}
	slices.Sort(values)
	return values, nil
}

func rackByNodeName(obj any) ([]string, error) {
	rack, ok := obj.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack index received %T", obj)
	}
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef == nil || slot.NodeRef.Name == "" {
			continue
		}
		if _, exists := seen[slot.NodeRef.Name]; exists {
			continue
		}
		seen[slot.NodeRef.Name] = struct{}{}
		values = append(values, slot.NodeRef.Name)
	}
	slices.Sort(values)
	return values, nil
}

// Cache is the read-only informer snapshot consumed by a reconciliation.
type Cache interface {
	Inventory(name string) (*mokkav1alpha1.SGPUInventory, error)
	Inventories() ([]*mokkav1alpha1.SGPUInventory, error)
	Profile(name string) (*mokkav1alpha1.SGPUProfile, error)
	Rack(name string) (*mokkav1alpha1.SGPURack, error)
	Racks() ([]*mokkav1alpha1.SGPURack, error)
	RacksByInventoryUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error)
	RacksByInventoryGroup(uid types.UID, group string) ([]*mokkav1alpha1.SGPURack, error)
	Nodes() ([]*corev1.Node, error)
}

// ListerCache adapts generated listers and informer indexes to Cache. Its List
// methods read only local informer storage and never call the API server.
type ListerCache struct {
	inventories mokkalisters.SGPUInventoryLister
	profiles    mokkalisters.SGPUProfileLister
	racks       cache.Indexer
	nodes       NodeLister
}

type NodeLister interface {
	List(selector labels.Selector) ([]*corev1.Node, error)
}

func NewListerCache(
	inventories mokkalisters.SGPUInventoryLister,
	profiles mokkalisters.SGPUProfileLister,
	racks cache.Indexer,
	nodes NodeLister,
) *ListerCache {
	return &ListerCache{inventories: inventories, profiles: profiles, racks: racks, nodes: nodes}
}

func (c *ListerCache) Inventory(name string) (*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.Get(name)
}

func (c *ListerCache) Inventories() ([]*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.List(labels.Everything())
}

func (c *ListerCache) Profile(name string) (*mokkav1alpha1.SGPUProfile, error) {
	return c.profiles.Get(name)
}

func (c *ListerCache) Rack(name string) (*mokkav1alpha1.SGPURack, error) {
	obj, exists, err := c.racks.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), name)
	}
	rack, ok := obj.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack cache contained %T", obj)
	}
	return rack, nil
}

func (c *ListerCache) Racks() ([]*mokkav1alpha1.SGPURack, error) {
	objects := c.racks.List()
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, obj := range objects {
		rack, ok := obj.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", obj)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

func (c *ListerCache) RacksByInventoryUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(RackByInventoryUIDIndex, string(uid))
	if err != nil {
		return nil, err
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, obj := range objects {
		rack, ok := obj.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", obj)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

func (c *ListerCache) RacksByInventoryGroup(uid types.UID, group string) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(RackByInventoryGroupIndex, InventoryGroupIndexKey(uid, group))
	if err != nil {
		return nil, err
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, obj := range objects {
		rack, ok := obj.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", obj)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

func (c *ListerCache) Nodes() ([]*corev1.Node, error) {
	return c.nodes.List(labels.Everything())
}

// NewNodeLister adapts a Node informer indexer without requiring a generated
// Kubernetes informer package in this repository's vendored dependency set.
func NewNodeLister(indexer cache.Indexer) NodeLister {
	return listers.New[*corev1.Node](indexer, corev1.Resource("nodes"))
}
