// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
)

//nolint:revive // These names define one cohesive informer index contract.
const (
	// InventoryByProfileNameIndex identifies direct profile dependents.
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

// Indexers provides all rack lookups used by reconciliation and later
// projection event routing.
func Indexers() cache.Indexers {
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
	for _, slot := range rack.Spec.Nodes {
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
	for _, slot := range rack.Spec.Nodes {
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
	Profile(name string) (*mokkav1alpha1.SGPURackProfile, error)
	Rack(name string) (*mokkav1alpha1.SGPURack, error)
	Racks() ([]*mokkav1alpha1.SGPURack, error)
	RacksByInventoryUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error)
	RacksByInventoryGroup(uid types.UID, group string) ([]*mokkav1alpha1.SGPURack, error)
	AllocationNodeGeneration() uint64
	AllocationNodes() ([]allocate.Node, error)
}
