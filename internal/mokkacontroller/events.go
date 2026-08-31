// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

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
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
)

// rackConflictWaiters indexes only name collisions observed by reconciliation.
// Initial Inventory events rebuild this derived state without expanding every
// desired rack coordinate in memory.
type rackConflictWaiters struct {
	mu          sync.RWMutex
	byRack      map[string]map[allocate.GroupKey]struct{}
	byGroup     map[allocate.GroupKey]map[string]struct{}
	byInventory map[string]map[allocate.GroupKey]struct{}
}

func newRackConflictWaiters() *rackConflictWaiters {
	return &rackConflictWaiters{
		byRack:      make(map[string]map[allocate.GroupKey]struct{}),
		byGroup:     make(map[allocate.GroupKey]map[string]struct{}),
		byInventory: make(map[string]map[allocate.GroupKey]struct{}),
	}
}

func (r *rackConflictWaiters) replaceInventory(
	inventory *mokkav1alpha1.SGPUInventory,
	conflicts []controllerack.OwnershipConflict,
) {
	if inventory == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeInventoryLocked(inventory.Name, "", true)
	for _, conflict := range conflicts {
		if conflict.RackName == "" || conflict.RackGroup == "" {
			continue
		}
		r.addLocked(conflict.RackName, groupKey(inventory, conflict.RackGroup))
	}
}

func (r *rackConflictWaiters) replaceGroup(
	key allocate.GroupKey,
	conflicts []controllerack.OwnershipConflict,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeGroupLocked(key)
	for _, conflict := range conflicts {
		if conflict.RackName != "" && conflict.RackGroup == key.RackGroup {
			r.addLocked(conflict.RackName, key)
		}
	}
}

func (r *rackConflictWaiters) retainInventory(inventory *mokkav1alpha1.SGPUInventory) {
	if inventory == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.byInventory[inventory.Name] {
		if key.InventoryUID != inventory.UID {
			r.removeGroupLocked(key)
		}
	}
}

func (r *rackConflictWaiters) removeInventory(inventory *mokkav1alpha1.SGPUInventory) {
	if inventory == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeInventoryLocked(inventory.Name, inventory.UID, false)
}

func (r *rackConflictWaiters) removeInventoryLocked(name string, uid types.UID, allUIDs bool) {
	for key := range r.byInventory[name] {
		if allUIDs || key.InventoryUID == uid {
			r.removeGroupLocked(key)
		}
	}
}

func (r *rackConflictWaiters) removeGroupLocked(key allocate.GroupKey) {
	for name := range r.byGroup[key] {
		waiters := r.byRack[name]
		delete(waiters, key)
		if len(waiters) == 0 {
			delete(r.byRack, name)
		}
	}
	delete(r.byGroup, key)
	groups := r.byInventory[key.InventoryName]
	delete(groups, key)
	if len(groups) == 0 {
		delete(r.byInventory, key.InventoryName)
	}
}

func (r *rackConflictWaiters) addLocked(name string, key allocate.GroupKey) {
	waiters := r.byRack[name]
	if waiters == nil {
		waiters = make(map[allocate.GroupKey]struct{})
		r.byRack[name] = waiters
	}
	waiters[key] = struct{}{}
	names := r.byGroup[key]
	if names == nil {
		names = make(map[string]struct{})
		r.byGroup[key] = names
	}
	names[name] = struct{}{}
	groups := r.byInventory[key.InventoryName]
	if groups == nil {
		groups = make(map[allocate.GroupKey]struct{})
		r.byInventory[key.InventoryName] = groups
	}
	groups[key] = struct{}{}
}

func (r *rackConflictWaiters) waiters(name string) []allocate.GroupKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	waiters := make([]allocate.GroupKey, 0, len(r.byRack[name]))
	for key := range r.byRack[name] {
		waiters = append(waiters, key)
	}
	sortGroupKeys(waiters)
	return waiters
}

func (r *rackConflictWaiters) size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	waiters := 0
	for _, groups := range r.byRack {
		waiters += len(groups)
	}
	return waiters
}

type placementRegistry struct {
	mu          sync.RWMutex
	byInventory map[string]placementInventory
	active      map[allocate.GroupKey]labels.Selector
}

type placementInventory struct {
	inventory *mokkav1alpha1.SGPUInventory
	groups    map[allocate.GroupKey]labels.Selector
}

type placementInventoryKey struct {
	name string
	uid  types.UID
}

func newPlacementRegistry() *placementRegistry {
	return &placementRegistry{
		byInventory: make(map[string]placementInventory),
		active:      make(map[allocate.GroupKey]labels.Selector),
	}
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
	r.byInventory[inventory.Name] = placementInventory{inventory: inventory, groups: groups}
	r.rebuildActiveLocked()
	r.mu.Unlock()
}

func (r *placementRegistry) remove(inventory *mokkav1alpha1.SGPUInventory) ([]placementInventoryKey, bool) {
	if inventory == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, exists := r.byInventory[inventory.Name]
	if !exists || old.inventory.UID != inventory.UID {
		return nil, false
	}
	delete(r.byInventory, inventory.Name)
	r.rebuildActiveLocked()
	return r.inventoryKeysLocked(), true
}

func (r *placementRegistry) rebuildActiveLocked() {
	inventories := make([]*mokkav1alpha1.SGPUInventory, 0, len(r.byInventory))
	for _, registered := range r.byInventory {
		inventories = append(inventories, registered.inventory)
	}
	admitted := controllerack.AdmittedRackGroupInventoryUIDs(inventories)
	active := make(map[allocate.GroupKey]labels.Selector, controllerack.MaxRackGroups)
	for _, registered := range r.byInventory {
		if _, ok := admitted[registered.inventory.UID]; !ok {
			continue
		}
		for key, selector := range registered.groups {
			active[key] = selector
		}
	}
	r.active = active
}

func (r *placementRegistry) inventoryKeysLocked() []placementInventoryKey {
	inventories := make([]placementInventoryKey, 0, len(r.byInventory))
	for name, registered := range r.byInventory {
		inventories = append(inventories, placementInventoryKey{name: name, uid: registered.inventory.UID})
	}
	slices.SortFunc(inventories, func(a, b placementInventoryKey) int {
		if order := cmp.Compare(a.name, b.name); order != 0 {
			return order
		}
		return cmp.Compare(string(a.uid), string(b.uid))
	})
	return inventories
}

func (r *placementRegistry) matching(node *corev1.Node) []allocate.GroupKey {
	if node == nil || node.Labels[allocate.EligibleNodeLabel] != "true" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	matches := make([]allocate.GroupKey, 0, 1)
	set := labels.Set(node.Labels)
	for key, selector := range r.active {
		if selector.Matches(set) {
			matches = append(matches, key)
		}
	}
	sortGroupKeys(matches)
	return matches
}

type eventRouter struct {
	inventories        cache.Indexer
	racks              cache.Indexer
	registry           *placementRegistry
	waiters            *rackConflictWaiters
	queues             *queues
	invalidate         func()
	invalidateCapacity func()
	capacityWakeup     func() string
	observeRackStatus  func(*mokkav1alpha1.SGPURack)
	forgetRackStatus   func(string, types.UID)
}

func newEventRouter(
	inventories, racks cache.Indexer,
	registry *placementRegistry,
	queues *queues,
	invalidators ...func(),
) *eventRouter {
	var invalidate func()
	if len(invalidators) > 0 {
		invalidate = invalidators[0]
	}
	var invalidateCapacity func()
	if len(invalidators) > 1 {
		invalidateCapacity = invalidators[1]
	}
	return &eventRouter{
		inventories: inventories, racks: racks, registry: registry, waiters: newRackConflictWaiters(),
		queues: queues, invalidate: invalidate, invalidateCapacity: invalidateCapacity,
	}
}

func (r *eventRouter) invalidateAllocation() {
	if r.invalidate != nil {
		r.invalidate()
	}
}

func (r *eventRouter) invalidateTopology() {
	r.invalidateAllocation()
	if r.invalidateCapacity != nil {
		r.invalidateCapacity()
	}
}

func (r *eventRouter) invalidateRackCapacity() {
	var wakeup string
	if r.capacityWakeup != nil {
		wakeup = r.capacityWakeup()
	}
	r.invalidateTopology()
	if wakeup != "" {
		r.queues.inventories.Add(wakeup)
	}
}

func (r *eventRouter) inventoryAdd(object any) {
	inventory, ok := eventObject[*mokkav1alpha1.SGPUInventory](object)
	if !ok {
		return
	}
	r.invalidateTopology()
	r.registry.replace(inventory)
	r.waiters.retainInventory(inventory)
	// The newcomer drives the lazy admission rebuild. The rebuilt snapshot
	// separately reports every incumbent displaced by timestamp/name/UID order,
	// while initial informer delivery remains linear before a snapshot exists.
	r.routeInventory(inventory)
}

func (r *eventRouter) inventoryUpdate(oldObject, newObject any) {
	oldInventory, oldOK := eventObject[*mokkav1alpha1.SGPUInventory](oldObject)
	newInventory, newOK := eventObject[*mokkav1alpha1.SGPUInventory](newObject)
	if !oldOK || !newOK {
		return
	}
	topologyChanged := !inventoryAllocationUnchanged(oldInventory, newInventory)
	if topologyChanged {
		r.invalidateTopology()
	}
	if inventoryUnchanged(oldInventory, newInventory) {
		return
	}
	if oldInventory.Name != newInventory.Name || oldInventory.UID != newInventory.UID ||
		!equality.Semantic.DeepEqual(oldInventory.Spec, newInventory.Spec) ||
		!equality.Semantic.DeepEqual(oldInventory.DeletionTimestamp, newInventory.DeletionTimestamp) {
		r.waiters.removeInventory(oldInventory)
	}
	r.registry.replace(newInventory)
	r.waiters.retainInventory(newInventory)
	if topologyChanged {
		r.routeAllInventories()
	} else {
		r.routeInventory(newInventory)
	}
}

func (r *eventRouter) inventoryDelete(object any) {
	inventory, ok := eventObject[*mokkav1alpha1.SGPUInventory](object)
	if !ok {
		return
	}
	r.invalidateTopology()
	survivors, removed := r.registry.remove(inventory)
	r.waiters.removeInventory(inventory)
	r.routeInventory(inventory)
	if !removed {
		return
	}
	for _, survivor := range survivors {
		r.routeInventoryKey(survivor)
	}
}

func (r *eventRouter) routeInventory(inventory *mokkav1alpha1.SGPUInventory) {
	r.routeInventoryKey(placementInventoryKey{name: inventory.Name, uid: inventory.UID})
}

func (r *eventRouter) routeInventoryKey(inventory placementInventoryKey) {
	r.queues.inventories.Add(inventory.name)
	r.queues.addStatus(statusKey{kind: statusInventory, name: inventory.name, uid: inventory.uid})
}

func (r *eventRouter) routeAllInventories() {
	for _, object := range r.inventories.List() {
		inventory, ok := object.(*mokkav1alpha1.SGPUInventory)
		if !ok {
			continue
		}
		r.routeInventory(inventory)
	}
}

func (r *eventRouter) profileAdd(object any) {
	_, ok := eventObject[*mokkav1alpha1.SGPURackProfile](object)
	if ok {
		r.invalidateTopology()
		r.routeAllInventories()
	}
}

func (r *eventRouter) profileUpdate(oldObject, newObject any) {
	oldProfile, oldOK := eventObject[*mokkav1alpha1.SGPURackProfile](oldObject)
	newProfile, newOK := eventObject[*mokkav1alpha1.SGPURackProfile](newObject)
	if !oldOK || !newOK {
		return
	}
	if !profileAllocationUnchanged(oldProfile, newProfile) {
		r.invalidateTopology()
	}
	if profileUnchanged(oldProfile, newProfile) {
		return
	}
	r.routeAllInventories()
}

func (r *eventRouter) profileDelete(object any) {
	_, ok := eventObject[*mokkav1alpha1.SGPURackProfile](object)
	if ok {
		r.invalidateTopology()
		r.routeAllInventories()
	}
}

func (r *eventRouter) nodeAdd(object any) {
	node, ok := eventObject[*corev1.Node](object)
	if !ok {
		return
	}
	r.routeNodeWithBindings(node, nil, nil, true)
}

func (r *eventRouter) nodeUpdate(oldObject, newObject any) {
	oldNode, oldOK := eventObject[*corev1.Node](oldObject)
	newNode, newOK := eventObject[*corev1.Node](newObject)
	if !oldOK || !newOK || nodeUnchanged(oldNode, newNode) {
		return
	}
	bound := r.boundRacks(newNode.Name, newNode.UID)
	if projectionOnlyNodeUpdate(oldNode, newNode) && projectionsMatchBindings(newNode, bound) {
		for _, rack := range bound {
			r.queues.addStatus(statusKey{kind: statusRack, name: rack.Name, uid: rack.UID})
			r.queues.addStatus(statusKey{
				kind: statusInventory, name: rack.Spec.InventoryRef.Name, uid: rack.Spec.InventoryRef.UID,
			})
		}
		return
	}
	groups := append(r.registry.matching(oldNode), r.registry.matching(newNode)...)
	r.routeNodeWithBindings(newNode, uniqueGroupKeys(groups), bound, false)
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
		for _, slot := range rack.Spec.Nodes {
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

func (r *eventRouter) routeNodeWithBindings(
	node *corev1.Node,
	groups []allocate.GroupKey,
	bound []*mokkav1alpha1.SGPURack,
	fresh bool,
) {
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
		var freshSlots map[int32]types.UID
		if fresh {
			freshSlots = make(map[int32]types.UID)
			for _, slot := range rack.Spec.Nodes {
				if slot.NodeRef != nil && slot.NodeRef.Name == node.Name && slot.NodeRef.UID == node.UID {
					freshSlots[slot.Index] = slot.NodeRef.UID
				}
			}
		}
		r.routeRackCurrent(rack, false, freshSlots)
	}
}

func (r *eventRouter) rackAdd(object any) {
	rack, ok := eventObject[*mokkav1alpha1.SGPURack](object)
	if ok {
		if r.observeRackStatus != nil {
			r.observeRackStatus(rack)
		}
		r.invalidateRackCapacity()
		r.routeRackWaiters(rack.Name)
		fresh := make(map[int32]types.UID)
		freeSlot := false
		for _, slot := range rack.Spec.Nodes {
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
	if !oldOK || !newOK {
		return
	}
	if r.observeRackStatus != nil {
		r.observeRackStatus(newRack)
	}
	if !rackCapacityUnchanged(oldRack, newRack) {
		r.invalidateRackCapacity()
	} else if !rackAllocationUnchanged(oldRack, newRack) {
		r.invalidateAllocation()
	}
	if rackUnchanged(oldRack, newRack) {
		return
	}
	deletionStarted := oldRack.DeletionTimestamp == nil && newRack.DeletionTimestamp != nil
	if rackOwnershipChanged(oldRack, newRack) || deletionStarted {
		r.routeRackWaiters(oldRack.Name)
		if oldRack.Name != newRack.Name {
			r.routeRackWaiters(newRack.Name)
		}
	}
	newBindings := make(map[int32]types.UID, len(newRack.Spec.Nodes))
	for _, slot := range newRack.Spec.Nodes {
		if slot.NodeRef != nil {
			newBindings[slot.Index] = slot.NodeRef.UID
		}
	}
	oldBindings := make(map[int32]types.UID, len(oldRack.Spec.Nodes))
	for _, slot := range oldRack.Spec.Nodes {
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
		r.routeRackInventory(newRack)
		r.routeRackCurrent(newRack, false, nil)
		return
	}
	templateChanged := !rackTemplateUnchanged(oldRack, newRack)
	if templateChanged {
		r.routeRackInventory(oldRack)
		r.routeRackInventory(newRack)
	}
	fresh := make(map[int32]types.UID)
	for index, uid := range newBindings {
		if oldBindings[index] != uid {
			fresh[index] = uid
		}
	}
	r.routeRackCurrent(newRack, !templateChanged && !rackBindingsUnchanged(oldRack, newRack), fresh)
}

func (r *eventRouter) rackDelete(object any) {
	rack, ok := eventObject[*mokkav1alpha1.SGPURack](object)
	if !ok {
		return
	}
	if r.forgetRackStatus != nil {
		r.forgetRackStatus(rack.Name, rack.UID)
	}
	r.invalidateRackCapacity()
	for _, slot := range rack.Spec.Nodes {
		if slot.NodeRef == nil {
			continue
		}
		cleanup := cleanupFor(rack, slot, controllerack.CleanupRackDeleting)
		r.queues.projections.Add(projectionKey{mode: projectionCleanup, cleanup: cleanup})
	}
	if !r.currentRackDelete(rack) {
		return
	}
	r.routeRackWaiters(rack.Name)
	if r.rackDesired(rack) {
		r.routeRackInventory(rack)
	}
}

func (r *eventRouter) routeRackWaiters(name string) {
	waiters := r.waiters.waiters(name)
	for _, key := range waiters {
		r.queues.inventories.Add(key.InventoryName)
		r.queues.addStatus(statusKey{
			kind: statusInventory, name: key.InventoryName, uid: key.InventoryUID,
		})
	}
}

func rackOwnerGroup(rack *mokkav1alpha1.SGPURack) (allocate.GroupKey, bool) {
	owner := metav1.GetControllerOf(rack)
	if owner == nil || owner.APIVersion != mokkav1alpha1.SchemeGroupVersion.String() || owner.Kind != "SGPUInventory" {
		return allocate.GroupKey{}, false
	}
	if owner.Name == "" || owner.UID == "" || rack.Spec.Identity.RackGroup == "" {
		return allocate.GroupKey{}, false
	}
	return allocate.GroupKey{
		InventoryName: owner.Name,
		InventoryUID:  owner.UID,
		RackGroup:     rack.Spec.Identity.RackGroup,
	}, true
}

func (r *eventRouter) currentRackDelete(deleted *mokkav1alpha1.SGPURack) bool {
	object, exists, err := r.racks.GetByKey(deleted.Name)
	if err != nil {
		klog.Background().Error(err, "Check Rack delete against informer cache", "rack", deleted.Name)
		return true
	}
	if !exists {
		return true
	}
	current, ok := object.(*mokkav1alpha1.SGPURack)
	return !ok || current.UID == deleted.UID
}

func rackOwnershipChanged(old, current *mokkav1alpha1.SGPURack) bool {
	return !equality.Semantic.DeepEqual(old.OwnerReferences, current.OwnerReferences) ||
		old.Spec.InventoryRef != current.Spec.InventoryRef
}

func (r *eventRouter) routeRackCurrent(rack *mokkav1alpha1.SGPURack, reconcile bool, fresh map[int32]types.UID) {
	if reconcile {
		r.routeRackDependencies(rack)
	}
	if rackOwnedByReference(rack) {
		for _, slot := range rack.Spec.Nodes {
			if slot.NodeRef != nil {
				r.queues.projections.Add(projectionKey{
					mode: projectionApply, rackName: rack.Name, nodeIndex: slot.Index,
					fresh: fresh[slot.Index] == slot.NodeRef.UID,
				})
			}
		}
	}
	r.queues.addStatus(statusKey{kind: statusRack, name: rack.Name, uid: rack.UID})
}

func rackOwnedByReference(rack *mokkav1alpha1.SGPURack) bool {
	key, owned := rackOwnerGroup(rack)
	return owned && key.InventoryName == rack.Spec.InventoryRef.Name && key.InventoryUID == rack.Spec.InventoryRef.UID
}

func (r *eventRouter) rackDesired(rack *mokkav1alpha1.SGPURack) bool {
	key, owned := rackOwnerGroup(rack)
	if !owned {
		return false
	}
	object, exists, err := r.inventories.GetByKey(key.InventoryName)
	if err != nil || !exists {
		return false
	}
	inventory, ok := object.(*mokkav1alpha1.SGPUInventory)
	if !ok || inventory.UID != key.InventoryUID || inventory.DeletionTimestamp != nil {
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
	key, owned := rackOwnerGroup(rack)
	if !owned {
		return
	}
	r.queues.groups.Add(key)
	r.queues.addStatus(statusKey{
		kind: statusInventory, name: key.InventoryName, uid: key.InventoryUID,
	})
}

func (r *eventRouter) routeRackInventory(rack *mokkav1alpha1.SGPURack) {
	key, owned := rackOwnerGroup(rack)
	if !owned {
		return
	}
	r.queues.inventories.Add(key.InventoryName)
	r.queues.addStatus(statusKey{
		kind: statusInventory, name: key.InventoryName, uid: key.InventoryUID,
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

func cleanupFor(rack *mokkav1alpha1.SGPURack, slot mokkav1alpha1.SGPURackNode, reason controllerack.CleanupReason) controllerack.CleanupNeeded {
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
				NodeIndex: slot.Index,
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

func inventoryAllocationUnchanged(old, current *mokkav1alpha1.SGPUInventory) bool {
	return old.Name == current.Name && old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func profileUnchanged(old, current *mokkav1alpha1.SGPURackProfile) bool {
	return old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func profileAllocationUnchanged(old, current *mokkav1alpha1.SGPURackProfile) bool {
	return old.Name == current.Name && old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func rackUnchanged(old, current *mokkav1alpha1.SGPURack) bool {
	return old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.OwnerReferences, current.OwnerReferences) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func rackAllocationUnchanged(old, current *mokkav1alpha1.SGPURack) bool {
	return old.Name == current.Name && old.UID == current.UID &&
		equality.Semantic.DeepEqual(old.Spec, current.Spec) &&
		equality.Semantic.DeepEqual(old.OwnerReferences, current.OwnerReferences) &&
		equality.Semantic.DeepEqual(old.DeletionTimestamp, current.DeletionTimestamp)
}

func rackCapacityUnchanged(old, current *mokkav1alpha1.SGPURack) bool {
	if old.Name != current.Name || old.UID != current.UID ||
		old.Spec.InventoryRef != current.Spec.InventoryRef ||
		old.Spec.Identity.RackGroup != current.Spec.Identity.RackGroup ||
		!equality.Semantic.DeepEqual(old.OwnerReferences, current.OwnerReferences) ||
		len(old.Spec.Nodes) != len(current.Spec.Nodes) {
		return false
	}
	oldGPUs := 0
	for _, node := range old.Spec.Nodes {
		oldGPUs += len(node.GPUs)
	}
	currentGPUs := 0
	for _, node := range current.Spec.Nodes {
		currentGPUs += len(node.GPUs)
	}
	return oldGPUs == currentGPUs
}

func rackTemplateUnchanged(old, current *mokkav1alpha1.SGPURack) bool {
	oldSpec := old.Spec.DeepCopy()
	currentSpec := current.Spec.DeepCopy()
	for index := range oldSpec.Nodes {
		oldSpec.Nodes[index].NodeRef = nil
	}
	for index := range currentSpec.Nodes {
		currentSpec.Nodes[index].NodeRef = nil
	}
	return equality.Semantic.DeepEqual(oldSpec, currentSpec) &&
		equality.Semantic.DeepEqual(old.OwnerReferences, current.OwnerReferences)
}

func rackBindingsUnchanged(old, current *mokkav1alpha1.SGPURack) bool {
	oldBindings := make(map[int32]*mokkav1alpha1.SGPUNodeReference, len(old.Spec.Nodes))
	for _, slot := range old.Spec.Nodes {
		oldBindings[slot.Index] = slot.NodeRef
	}
	currentBindings := make(map[int32]*mokkav1alpha1.SGPUNodeReference, len(current.Spec.Nodes))
	for _, slot := range current.Spec.Nodes {
		currentBindings[slot.Index] = slot.NodeRef
	}
	return equality.Semantic.DeepEqual(oldBindings, currentBindings)
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
		for i := range rack.Spec.Nodes {
			slot := &rack.Spec.Nodes[i]
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
