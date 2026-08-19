// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"cmp"
	"fmt"
	"slices"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
)

type placementRegistry struct {
	mu          sync.RWMutex
	byInventory map[string]map[allocate.GroupKey]labels.Selector
}

func newPlacementRegistry() *placementRegistry {
	return &placementRegistry{byInventory: make(map[string]map[allocate.GroupKey]labels.Selector)}
}

func (r *placementRegistry) replace(inventory *mokkav1alpha1.SGPUInventory) {
	if inventory == nil {
		return
	}
	groups := make(map[allocate.GroupKey]labels.Selector, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		key := groupKey(inventory, group.ID)
		selector := labels.Everything()
		if group.Placement != nil && group.Placement.NodeSelector != nil {
			var err error
			selector, err = allocate.CompilePlacementSelector(group.Placement.NodeSelector)
			if err != nil {
				selector = labels.Nothing()
			}
		}
		groups[key] = selector
	}
	r.mu.Lock()
	r.byInventory[inventory.Name] = groups
	r.mu.Unlock()
}

func (r *placementRegistry) remove(inventory *mokkav1alpha1.SGPUInventory) {
	if inventory == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.byInventory[inventory.Name]
	if len(old) > 0 {
		for key := range old {
			if key.InventoryUID != inventory.UID {
				return
			}
		}
	}
	delete(r.byInventory, inventory.Name)
}

func (r *placementRegistry) matching(node *corev1.Node) []allocate.GroupKey {
	if node == nil || node.Labels[allocate.EligibleNodeLabel] != "true" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	matches := make([]allocate.GroupKey, 0, 1)
	set := labels.Set(node.Labels)
	for _, groups := range r.byInventory {
		for key, selector := range groups {
			if selector.Matches(set) {
				matches = append(matches, key)
			}
		}
	}
	sortGroupKeys(matches)
	return matches
}

type eventRouter struct {
	inventories cache.Indexer
	racks       cache.Indexer
	registry    *placementRegistry
	queues      *queues
}

func newEventRouter(inventories, racks cache.Indexer, registry *placementRegistry, queues *queues) *eventRouter {
	return &eventRouter{inventories: inventories, racks: racks, registry: registry, queues: queues}
}

func (r *eventRouter) inventoryAdd(object any) {
	inventory, ok := eventObject[*mokkav1alpha1.SGPUInventory](object)
	if !ok {
		return
	}
	r.registry.replace(inventory)
	r.routeInventory(inventory)
}

func (r *eventRouter) inventoryUpdate(oldObject, newObject any) {
	oldInventory, oldOK := eventObject[*mokkav1alpha1.SGPUInventory](oldObject)
	newInventory, newOK := eventObject[*mokkav1alpha1.SGPUInventory](newObject)
	if !oldOK || !newOK || inventoryUnchanged(oldInventory, newInventory) {
		return
	}
	r.registry.replace(newInventory)
	r.routeInventory(newInventory)
}

func (r *eventRouter) inventoryDelete(object any) {
	inventory, ok := eventObject[*mokkav1alpha1.SGPUInventory](object)
	if !ok {
		return
	}
	r.registry.remove(inventory)
	r.routeInventory(inventory)
}

func (r *eventRouter) routeInventory(inventory *mokkav1alpha1.SGPUInventory) {
	r.queues.inventories.Add(inventory.Name)
	r.queues.addStatus(statusKey{kind: statusInventory, name: inventory.Name, uid: inventory.UID})
}

func (r *eventRouter) profileAdd(object any) {
	profile, ok := eventObject[*mokkav1alpha1.SGPUProfile](object)
	if ok {
		r.routeProfile(profile.Name)
	}
}

func (r *eventRouter) profileUpdate(oldObject, newObject any) {
	oldProfile, oldOK := eventObject[*mokkav1alpha1.SGPUProfile](oldObject)
	newProfile, newOK := eventObject[*mokkav1alpha1.SGPUProfile](newObject)
	if !oldOK || !newOK || profileUnchanged(oldProfile, newProfile) {
		return
	}
	r.routeProfile(oldProfile.Name)
	if oldProfile.Name != newProfile.Name {
		r.routeProfile(newProfile.Name)
	}
}

func (r *eventRouter) profileDelete(object any) {
	profile, ok := eventObject[*mokkav1alpha1.SGPUProfile](object)
	if ok {
		r.routeProfile(profile.Name)
	}
}

func (r *eventRouter) routeProfile(name string) {
	objects, err := r.inventories.ByIndex(controllerack.InventoryByProfileNameIndex, name)
	if err != nil {
		klog.Background().Error(err, "Look up inventories by profile", "profile", name)
		return
	}
	for _, object := range objects {
		inventory, ok := object.(*mokkav1alpha1.SGPUInventory)
		if !ok {
			continue
		}
		r.queues.inventories.Add(inventory.Name)
		r.queues.addStatus(statusKey{kind: statusInventory, name: inventory.Name, uid: inventory.UID})
	}
}

func (r *eventRouter) nodeAdd(object any) {
	node, ok := eventObject[*corev1.Node](object)
	if !ok {
		return
	}
	r.routeNode(node, nil)
}

func (r *eventRouter) nodeUpdate(oldObject, newObject any) {
	oldNode, oldOK := eventObject[*corev1.Node](oldObject)
	newNode, newOK := eventObject[*corev1.Node](newObject)
	if !oldOK || !newOK || nodeUnchanged(oldNode, newNode) {
		return
	}
	bound := r.boundRacks(newNode.Name, newNode.UID)
	if projectionOnlyNodeUpdate(oldNode, newNode) && projectionsMatchBindings(newNode, bound) {
		return
	}
	groups := append(r.registry.matching(oldNode), r.registry.matching(newNode)...)
	r.routeNodeWithBindings(newNode, uniqueGroupKeys(groups), bound)
}

func (r *eventRouter) nodeDelete(object any) {
	node, ok := eventObject[*corev1.Node](object)
	if !ok {
		return
	}
	groups := r.registry.matching(node)
	bound := r.boundRacks(node.Name, node.UID)
	deferred := make(map[allocate.GroupKey]struct{})
	for _, rack := range bound {
		for _, slot := range rack.Spec.Slots {
			if slot.NodeRef == nil || slot.NodeRef.Name != node.Name || slot.NodeRef.UID != node.UID {
				continue
			}
			cleanup := cleanupFor(rack, slot, controllerack.CleanupNodeIneligible)
			r.queues.projections.Add(projectionKey{mode: projectionCleanup, cleanup: cleanup})
			deferred[cleanup.Binding.Coordinate.Group] = struct{}{}
		}
	}
	for _, key := range groups {
		if _, waitForCleanup := deferred[key]; !waitForCleanup {
			r.queues.groups.Add(key)
		}
		r.queues.addStatus(statusKey{kind: statusInventory, name: key.InventoryName, uid: key.InventoryUID})
	}
}

func (r *eventRouter) routeNode(node *corev1.Node, groups []allocate.GroupKey) {
	r.routeNodeWithBindings(node, groups, nil)
}

func (r *eventRouter) routeNodeWithBindings(node *corev1.Node, groups []allocate.GroupKey, bound []*mokkav1alpha1.SGPURack) {
	if groups == nil {
		groups = r.registry.matching(node)
	}
	for _, key := range groups {
		r.queues.groups.Add(key)
		r.queues.addStatus(statusKey{kind: statusInventory, name: key.InventoryName, uid: key.InventoryUID})
	}
	if bound == nil {
		bound = r.boundRacks(node.Name, node.UID)
	}
	for _, rack := range bound {
		r.routeRackCurrent(rack, false, nil)
	}
}

func (r *eventRouter) rackAdd(object any) {
	rack, ok := eventObject[*mokkav1alpha1.SGPURack](object)
	if ok {
		fresh := make(map[int32]types.UID)
		freeSlot := false
		for _, slot := range rack.Spec.Slots {
			if slot.NodeRef == nil {
				freeSlot = true
				continue
			}
			fresh[slot.Index] = slot.NodeRef.UID
		}
		r.routeRackCurrent(rack, freeSlot || !rackOwnedByReference(rack), fresh)
	}
}

//nolint:cyclop // Rack updates route independent cleanup, projection, allocation, and status edges.
func (r *eventRouter) rackUpdate(oldObject, newObject any) {
	oldRack, oldOK := eventObject[*mokkav1alpha1.SGPURack](oldObject)
	newRack, newOK := eventObject[*mokkav1alpha1.SGPURack](newObject)
	if !oldOK || !newOK || rackUnchanged(oldRack, newRack) {
		return
	}
	newBindings := make(map[int32]types.UID, len(newRack.Spec.Slots))
	for _, slot := range newRack.Spec.Slots {
		if slot.NodeRef != nil {
			newBindings[slot.Index] = slot.NodeRef.UID
		}
	}
	oldBindings := make(map[int32]types.UID, len(oldRack.Spec.Slots))
	for _, slot := range oldRack.Spec.Slots {
		if slot.NodeRef != nil {
			oldBindings[slot.Index] = slot.NodeRef.UID
		}
		if slot.NodeRef == nil || newBindings[slot.Index] == slot.NodeRef.UID {
			continue
		}
		cleanup := cleanupFor(oldRack, slot, controllerack.CleanupCapacityShrink)
		r.queues.projections.Add(projectionKey{mode: projectionCleanup, cleanup: cleanup})
	}
	if newRack.DeletionTimestamp != nil {
		r.queues.inventories.Add(newRack.Spec.InventoryRef.Name)
		r.routeRackCurrent(newRack, false, nil)
		return
	}
	fresh := make(map[int32]types.UID)
	for index, uid := range newBindings {
		if oldBindings[index] != uid {
			fresh[index] = uid
		}
	}
	r.routeRackCurrent(newRack, true, fresh)
}

func (r *eventRouter) rackDelete(object any) {
	rack, ok := eventObject[*mokkav1alpha1.SGPURack](object)
	if !ok {
		return
	}
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef == nil {
			continue
		}
		cleanup := cleanupFor(rack, slot, controllerack.CleanupRackDeleting)
		r.queues.projections.Add(projectionKey{mode: projectionCleanup, cleanup: cleanup})
	}
	if r.rackDesired(rack) {
		r.queues.inventories.Add(rack.Spec.InventoryRef.Name)
	}
}

func (r *eventRouter) routeRackCurrent(rack *mokkav1alpha1.SGPURack, reconcile bool, fresh map[int32]types.UID) {
	if reconcile {
		r.routeRackDependencies(rack)
	}
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef != nil {
			r.queues.projections.Add(projectionKey{
				mode: projectionApply, rackName: rack.Name, slotIndex: slot.Index,
				fresh: fresh[slot.Index] == slot.NodeRef.UID,
			})
		}
	}
	r.queues.addStatus(statusKey{kind: statusRack, name: rack.Name, uid: rack.UID})
}

func rackOwnedByReference(rack *mokkav1alpha1.SGPURack) bool {
	owner := metav1.GetControllerOf(rack)
	return owner != nil && owner.APIVersion == mokkav1alpha1.SchemeGroupVersion.String() &&
		owner.Kind == "SGPUInventory" && owner.Name == rack.Spec.InventoryRef.Name && owner.UID == rack.Spec.InventoryRef.UID
}

func (r *eventRouter) rackDesired(rack *mokkav1alpha1.SGPURack) bool {
	object, exists, err := r.inventories.GetByKey(rack.Spec.InventoryRef.Name)
	if err != nil || !exists {
		return false
	}
	inventory, ok := object.(*mokkav1alpha1.SGPUInventory)
	if !ok || inventory.UID != rack.Spec.InventoryRef.UID || inventory.DeletionTimestamp != nil {
		return false
	}
	for _, group := range inventory.Spec.RackGroups {
		if group.ID == rack.Spec.Identity.RackGroup {
			return rack.Spec.Identity.RackIndex >= 0 && rack.Spec.Identity.RackIndex < group.Count
		}
	}
	return false
}

func (r *eventRouter) routeRackDependencies(rack *mokkav1alpha1.SGPURack) {
	if rack.Spec.InventoryRef.Name == "" || rack.Spec.InventoryRef.UID == "" {
		return
	}
	r.queues.groups.Add(allocate.GroupKey{
		InventoryName: rack.Spec.InventoryRef.Name,
		InventoryUID:  rack.Spec.InventoryRef.UID,
		RackGroup:     rack.Spec.Identity.RackGroup,
	})
	r.queues.addStatus(statusKey{
		kind: statusInventory, name: rack.Spec.InventoryRef.Name, uid: rack.Spec.InventoryRef.UID,
	})
}

func (r *eventRouter) boundRacks(name string, uid types.UID) []*mokkav1alpha1.SGPURack {
	indexed := make(map[string]*mokkav1alpha1.SGPURack)
	for index, value := range map[string]string{
		controllerack.RackByNodeUIDIndex:  string(uid),
		controllerack.RackByNodeNameIndex: name,
	} {
		objects, err := r.racks.ByIndex(index, value)
		if err != nil {
			klog.Background().Error(err, "Look up racks for Node", "node", name, "index", index)
			continue
		}
		for _, object := range objects {
			if rack, ok := object.(*mokkav1alpha1.SGPURack); ok {
				indexed[rack.Name] = rack
			}
		}
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(indexed))
	for _, rack := range indexed {
		racks = append(racks, rack)
	}
	slices.SortFunc(racks, func(a, b *mokkav1alpha1.SGPURack) int { return cmp.Compare(a.Name, b.Name) })
	return racks
}

func cleanupFor(rack *mokkav1alpha1.SGPURack, slot mokkav1alpha1.SGPURackSlot, reason controllerack.CleanupReason) controllerack.CleanupNeeded {
	return controllerack.CleanupNeeded{
		RackName: rack.Name,
		RackUID:  rack.UID,
		Reason:   reason,
		Binding: allocate.Binding{
			Coordinate: allocate.Coordinate{
				Group: allocate.GroupKey{
					InventoryName: rack.Spec.InventoryRef.Name,
					InventoryUID:  rack.Spec.InventoryRef.UID,
					RackGroup:     rack.Spec.Identity.RackGroup,
				},
				RackIndex: rack.Spec.Identity.RackIndex,
				SlotIndex: slot.Index,
			},
			Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
		},
	}
}

func eventObject[T any](object any) (T, bool) {
	if typed, ok := object.(T); ok {
		return typed, true
	}
	switch tombstone := object.(type) {
	case cache.DeletedFinalStateUnknown:
		typed, ok := tombstone.Obj.(T)
		return typed, ok
	case *cache.DeletedFinalStateUnknown:
		typed, ok := tombstone.Obj.(T)
		return typed, ok
	default:
		var zero T
		klog.Background().Error(nil, "Unexpected informer event object", "type", fmt.Sprintf("%T", object))
		return zero, false
	}
}

func inventoryUnchanged(old, current *mokkav1alpha1.SGPUInventory) bool {
	return old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.Finalizers, current.Finalizers) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func profileUnchanged(old, current *mokkav1alpha1.SGPUProfile) bool {
	return old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func rackUnchanged(old, current *mokkav1alpha1.SGPURack) bool {
	return old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.Finalizers, current.Finalizers) &&
		equality.Semantic.DeepEqual(old.OwnerReferences, current.OwnerReferences) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func nodeUnchanged(old, current *corev1.Node) bool {
	return old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Labels, current.Labels) &&
		old.Annotations[controllerprojection.AssignmentAnnotation] == current.Annotations[controllerprojection.AssignmentAnnotation] &&
		equality.Semantic.DeepEqual(old.ManagedFields, current.ManagedFields) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func projectionOnlyNodeUpdate(old, current *corev1.Node) bool {
	return old.Name == current.Name && old.UID == current.UID &&
		labelsEqualExceptProjection(old.Labels, current.Labels) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func labelsEqualExceptProjection(old, current map[string]string) bool {
	for key, value := range old {
		if key == controllerprojection.AssignedLabel || key == controllerprojection.CliqueLabel {
			continue
		}
		if currentValue, exists := current[key]; !exists || currentValue != value {
			return false
		}
	}
	for key := range current {
		if key == controllerprojection.AssignedLabel || key == controllerprojection.CliqueLabel {
			continue
		}
		if _, exists := old[key]; !exists {
			return false
		}
	}
	return true
}

func projectionsMatchBindings(node *corev1.Node, racks []*mokkav1alpha1.SGPURack) bool {
	found := false
	for _, rack := range racks {
		for i := range rack.Spec.Slots {
			slot := &rack.Spec.Slots[i]
			if slot.NodeRef == nil || slot.NodeRef.Name != node.Name || slot.NodeRef.UID != node.UID {
				continue
			}
			found = true
			if !controllerprojection.MatchesBinding(node, rack, slot) {
				return false
			}
		}
	}
	return found
}

func groupKey(inventory *mokkav1alpha1.SGPUInventory, group string) allocate.GroupKey {
	return allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: group}
}

func uniqueGroupKeys(keys []allocate.GroupKey) []allocate.GroupKey {
	sortGroupKeys(keys)
	return slices.Compact(keys)
}

func sortGroupKeys(keys []allocate.GroupKey) {
	slices.SortFunc(keys, func(a, b allocate.GroupKey) int {
		if order := cmp.Compare(a.InventoryName, b.InventoryName); order != 0 {
			return order
		}
		if order := cmp.Compare(string(a.InventoryUID), string(b.InventoryUID)); order != 0 {
			return order
		}
		return cmp.Compare(a.RackGroup, b.RackGroup)
	})
}
