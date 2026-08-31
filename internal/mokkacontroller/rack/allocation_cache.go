// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
)

var errAllocationInputChanged = errors.New("allocation input changed during reconciliation")

type allocationRevision struct {
	topology uint64
	nodes    uint64
	capacity capacityRevision
}

// AllocationRevision identifies one complete allocation input revision.
type AllocationRevision struct {
	revision allocationRevision
}

type inventoryInstance struct {
	name string
	uid  types.UID
}

type allocationSnapshot struct {
	revision    allocationRevision
	groups      map[allocate.GroupKey]*allocate.Plan
	inventories map[inventoryInstance]*allocate.Plan
	stats       allocate.Stats
	err         error
}

// AllocationCacheStats exposes deterministic cache-work counters for scale
// contracts and diagnostics.
type AllocationCacheStats struct {
	Computations     uint64
	TopologyRevision uint64
	NodeGeneration   uint64
}

// AllocationCache owns one immutable, partitioned global allocation snapshot.
// Revisions replace the sole published snapshot, so older generations remain
// reachable only while active reconciles consume their group-sized views.
type AllocationCache struct {
	cache Cache
	// admission shares the exact capacity decision used by rack workers with
	// the global allocator.
	admission *CapacityAdmission

	topology     atomic.Uint64
	computations atomic.Uint64
	mu           sync.Mutex
	snapshot     *allocationSnapshot
	allocate     func(allocate.Input) (allocate.Plan, error)
}

// NewAllocationCache returns a coalescing allocation cache over informer data.
func NewAllocationCache(cache Cache) *AllocationCache {
	return &AllocationCache{
		cache: cache, admission: NewCapacityAdmission(cache), allocate: allocate.Allocate,
	}
}

// Invalidate advances the cheap topology revision after an allocation-relevant
// Inventory, Profile, or Rack informer event.
func (c *AllocationCache) Invalidate() {
	c.topology.Add(1)
	c.admission.Invalidate()
}

// InvalidateAllocation advances inputs that do not affect capacity admission,
// such as Rack bindings.
func (c *AllocationCache) InvalidateAllocation() {
	c.topology.Add(1)
}

// InvalidateCapacity advances Inventory/Profile admission and, through the
// combined revision, invalidates the allocation snapshot as well.
func (c *AllocationCache) InvalidateCapacity() {
	c.admission.Invalidate()
}

// Stats returns cache counters without retaining a snapshot.
func (c *AllocationCache) Stats() AllocationCacheStats {
	revision := c.revision()
	return AllocationCacheStats{
		Computations:     c.computations.Load(),
		TopologyRevision: revision.topology,
		NodeGeneration:   revision.nodes,
	}
}

// CapacityWakeup returns one deterministic candidate that can recompute a
// stale admission snapshot after durable rack capacity changes.
func (c *AllocationCache) CapacityWakeup() string {
	instance := c.admission.wakeup()
	if instance.name == "" {
		return ""
	}
	inventory, err := c.cache.Inventory(instance.name)
	if err != nil || inventory.UID != instance.uid || inventory.DeletionTimestamp != nil {
		return ""
	}
	return instance.name
}

// CapacityTransitions drains inventories whose admission or readiness state
// changed when the current admission snapshot replaced its predecessor.
func (c *AllocationCache) CapacityTransitions() []string {
	instances := c.admission.takeTransitions()
	transitions := make([]string, len(instances))
	for index := range instances {
		transitions[index] = instances[index].name
	}
	return transitions
}

// BindingDesired reports whether the current allocation policy retains an
// exact durable binding.
func (c *AllocationCache) BindingDesired(binding allocate.Binding) (bool, error) {
	desired, _, err := c.BindingDesiredRevision(binding)
	return desired, err
}

// BindingDesiredRevision reports the binding decision and the exact allocation
// revision from which it was computed.
func (c *AllocationCache) BindingDesiredRevision(binding allocate.Binding) (bool, AllocationRevision, error) {
	revision := c.revision()
	group := binding.Coordinate.Group
	plan, err := c.planRevision(revision, &group, instanceForGroup(group))
	if err != nil {
		return false, AllocationRevision{}, err
	}
	index, found := slices.BinarySearchFunc(plan.Retained, binding, compareAllocationBindings)
	return found && plan.Retained[index] == binding, AllocationRevision{revision: revision}, nil
}

// RevisionCurrent reports whether every allocation-relevant input still
// matches revision.
func (c *AllocationCache) RevisionCurrent(revision AllocationRevision) bool {
	return revision.revision == c.revision()
}

func (c *AllocationCache) revision() allocationRevision {
	return allocationRevision{
		topology: c.topology.Load(),
		nodes:    c.cache.AllocationNodeGeneration(),
		capacity: c.admission.currentRevision(),
	}
}

func (c *AllocationCache) plan(group *allocate.GroupKey, inventory inventoryInstance) (allocate.Plan, error) {
	return c.planRevision(c.revision(), group, inventory)
}

func (c *AllocationCache) planRevision(
	revision allocationRevision,
	group *allocate.GroupKey,
	inventory inventoryInstance,
) (allocate.Plan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if revision != c.revision() {
		return allocate.Plan{}, errAllocationInputChanged
	}
	if c.snapshot == nil || c.snapshot.revision != revision {
		input, err := allocationInputRevision(c.cache, c.admission, revision.capacity)
		var plan allocate.Plan
		if err == nil {
			plan, err = c.allocate(input)
			c.computations.Add(1)
		}
		if revision != c.revision() {
			return allocate.Plan{}, errAllocationInputChanged
		}
		if err != nil {
			c.snapshot = &allocationSnapshot{revision: revision, err: err}
		} else {
			c.snapshot = partitionAllocation(revision, input.Groups, plan)
		}
	}
	snapshot := c.snapshot
	if snapshot.err != nil {
		return allocate.Plan{}, snapshot.err
	}
	if group != nil {
		return snapshot.groupView(*group), nil
	}
	return snapshot.inventoryView(inventory), nil
}

func allocationInput(cache Cache) (allocate.Input, error) {
	admission := NewCapacityAdmission(cache)
	return allocationInputRevision(cache, admission, admission.currentRevision())
}

func allocationInputRevision(
	cache Cache,
	admission *CapacityAdmission,
	revision capacityRevision,
) (allocate.Input, error) {
	inventories, err := cache.Inventories()
	if err != nil {
		return allocate.Input{}, fmt.Errorf("list inventories from cache: %w", err)
	}
	groups, err := allocationGroups(cache, inventories, admission, revision)
	if err != nil {
		return allocate.Input{}, err
	}
	bindings, err := allocationBindings(cache, inventories)
	if err != nil {
		return allocate.Input{}, err
	}
	nodes, err := cache.AllocationNodes()
	if err != nil {
		return allocate.Input{}, fmt.Errorf("list Nodes from cache: %w", err)
	}
	return allocate.Input{Groups: groups, Nodes: nodes, Bindings: bindings}, nil
}

func allocationGroups(
	cache Cache,
	inventories []*mokkav1alpha1.SGPUInventory,
	admission *CapacityAdmission,
	revision capacityRevision,
) ([]allocate.Group, error) {
	groups := make([]allocate.Group, 0)
	for _, inventory := range inventories {
		resolved, err := materializedInventoryGroups(cache, inventory)
		if err != nil {
			return nil, err
		}
		if len(resolved) == 0 {
			continue
		}
		capacity, err := capacityForResolvedGroups(resolved)
		if err != nil {
			return nil, err
		}
		admitted, err := admission.admits(revision, inventory, capacity)
		if err != nil {
			return nil, err
		}
		if !admitted {
			continue
		}
		for _, group := range resolved {
			var selector *metav1.LabelSelector
			if group.group.Placement != nil {
				selector = group.group.Placement.NodeSelector
			}
			groups = append(groups, allocate.Group{
				Key: group.key, Selector: selector,
				Racks: group.group.Count, NodesPerRack: group.profile.Spec.Rack.NodesPerRack,
			})
		}
	}
	return groups, nil
}

func allocationBindings(
	cache Cache,
	inventories []*mokkav1alpha1.SGPUInventory,
) ([]allocate.Binding, error) {
	racks, err := cache.Racks()
	if err != nil {
		return nil, fmt.Errorf("list racks from cache: %w", err)
	}
	inventoriesByUID := make(
		map[types.UID]*mokkav1alpha1.SGPUInventory,
		min(len(inventories), int(MaxInventoryNodes)),
	)
	for _, inventory := range inventories {
		if inventory != nil {
			inventoriesByUID[inventory.UID] = inventory
		}
	}
	bindings := make([]allocate.Binding, 0)
	for _, rack := range racks {
		inventory := inventoriesByUID[rack.Spec.InventoryRef.UID]
		if inventory == nil || !controlledByInventory(rack, inventory) {
			continue
		}
		for _, slot := range rack.Spec.Nodes {
			if slot.NodeRef == nil {
				continue
			}
			bindings = append(bindings, allocate.Binding{
				Coordinate: allocate.Coordinate{
					Group: allocate.GroupKey{
						InventoryName: inventory.Name, InventoryUID: inventory.UID,
						RackGroup: rack.Spec.Identity.RackGroup,
					},
					RackIndex: rack.Spec.Identity.RackIndex, NodeIndex: slot.Index,
				},
				Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
			})
		}
	}
	return bindings, nil
}

func partitionAllocation(
	revision allocationRevision,
	groups []allocate.Group,
	global allocate.Plan,
) *allocationSnapshot {
	snapshot := &allocationSnapshot{
		revision:    revision,
		groups:      make(map[allocate.GroupKey]*allocate.Plan, len(groups)),
		inventories: make(map[inventoryInstance]*allocate.Plan),
		stats:       global.Stats,
	}
	for _, group := range groups {
		snapshot.ensureGroup(group.Key)
	}
	partitionBindings(snapshot, global)
	partitionPending(snapshot, global)
	partitionConflicts(snapshot, global.Conflicts)
	return snapshot
}

func partitionBindings(snapshot *allocationSnapshot, global allocate.Plan) {
	for _, binding := range global.Retained {
		view := snapshot.ensureInventory(binding.Coordinate.Group)
		view.Retained = append(view.Retained, binding)
	}
	for _, release := range global.Released {
		view := snapshot.ensureInventory(release.Binding.Coordinate.Group)
		view.Released = append(view.Released, release)
	}
	for _, binding := range global.Assigned {
		view := snapshot.ensureInventory(binding.Coordinate.Group)
		view.Assigned = append(view.Assigned, binding)
	}
	for _, binding := range global.Bindings {
		view := snapshot.ensureInventory(binding.Coordinate.Group)
		view.Bindings = append(view.Bindings, binding)
	}
	for _, inventory := range snapshot.inventories {
		partitionGroupSlice(snapshot, inventory.Retained,
			func(binding allocate.Binding) allocate.GroupKey { return binding.Coordinate.Group },
			func(plan *allocate.Plan, items []allocate.Binding) { plan.Retained = items },
		)
		partitionGroupSlice(snapshot, inventory.Released,
			func(release allocate.Release) allocate.GroupKey { return release.Binding.Coordinate.Group },
			func(plan *allocate.Plan, items []allocate.Release) { plan.Released = items },
		)
		partitionGroupSlice(snapshot, inventory.Assigned,
			func(binding allocate.Binding) allocate.GroupKey { return binding.Coordinate.Group },
			func(plan *allocate.Plan, items []allocate.Binding) { plan.Assigned = items },
		)
	}
}

func partitionPending(snapshot *allocationSnapshot, global allocate.Plan) {
	for index, node := range global.Pending {
		if index >= len(global.PendingGroups) {
			break
		}
		key := global.PendingGroups[index]
		view := snapshot.ensureGroup(key)
		view.Pending = append(view.Pending, node)
		view.PendingGroups = append(view.PendingGroups, key)
		inventory := snapshot.ensureInventory(key)
		inventory.Pending = append(inventory.Pending, node)
		inventory.PendingGroups = append(inventory.PendingGroups, key)
	}
}

func partitionConflicts(snapshot *allocationSnapshot, conflicts []allocate.Conflict) {
	for _, conflict := range conflicts {
		keys := conflictGroups(conflict)
		instances := make(map[inventoryInstance]struct{}, len(keys))
		for _, key := range keys {
			view := snapshot.ensureGroup(key)
			view.Conflicts = append(view.Conflicts, conflict)
			instances[instanceForGroup(key)] = struct{}{}
		}
		for instance := range instances {
			view := snapshot.ensureInventoryInstance(instance)
			view.Conflicts = append(view.Conflicts, conflict)
		}
	}
}

func (s *allocationSnapshot) ensureGroup(key allocate.GroupKey) *allocate.Plan {
	if plan := s.groups[key]; plan != nil {
		return plan
	}
	plan := &allocate.Plan{Stats: s.stats}
	s.groups[key] = plan
	s.ensureInventory(key)
	return plan
}

func (s *allocationSnapshot) ensureInventory(key allocate.GroupKey) *allocate.Plan {
	return s.ensureInventoryInstance(instanceForGroup(key))
}

func (s *allocationSnapshot) ensureInventoryInstance(instance inventoryInstance) *allocate.Plan {
	if plan := s.inventories[instance]; plan != nil {
		return plan
	}
	plan := &allocate.Plan{Stats: s.stats}
	s.inventories[instance] = plan
	return plan
}

func (s *allocationSnapshot) groupView(key allocate.GroupKey) allocate.Plan {
	if view := s.groups[key]; view != nil {
		result := *view
		result.Bindings = make([]allocate.Binding, 0, len(result.Retained)+len(result.Assigned))
		result.Bindings = append(result.Bindings, result.Retained...)
		result.Bindings = append(result.Bindings, result.Assigned...)
		return result
	}
	return allocate.Plan{Stats: s.stats}
}

func (s *allocationSnapshot) inventoryView(instance inventoryInstance) allocate.Plan {
	if view := s.inventories[instance]; view != nil {
		return *view
	}
	return allocate.Plan{Stats: s.stats}
}

func partitionGroupSlice[T any](
	snapshot *allocationSnapshot,
	items []T,
	group func(T) allocate.GroupKey,
	set func(*allocate.Plan, []T),
) {
	for start := 0; start < len(items); {
		key := group(items[start])
		end := start + 1
		for end < len(items) && group(items[end]) == key {
			end++
		}
		set(snapshot.ensureGroup(key), items[start:end])
		start = end
	}
}

func conflictGroups(conflict allocate.Conflict) []allocate.GroupKey {
	keys := slices.Clone(conflict.Candidates)
	for _, binding := range conflict.Bindings {
		keys = append(keys, binding.Coordinate.Group)
	}
	slices.SortFunc(keys, compareGroupKeys)
	return slices.Compact(keys)
}

func instanceForGroup(key allocate.GroupKey) inventoryInstance {
	return inventoryInstance{name: key.InventoryName, uid: key.InventoryUID}
}

func compareGroupKeys(a, b allocate.GroupKey) int {
	if order := cmp.Compare(a.InventoryName, b.InventoryName); order != 0 {
		return order
	}
	if order := cmp.Compare(string(a.InventoryUID), string(b.InventoryUID)); order != 0 {
		return order
	}
	return cmp.Compare(a.RackGroup, b.RackGroup)
}

func compareAllocationBindings(a, b allocate.Binding) int {
	if order := compareGroupKeys(a.Coordinate.Group, b.Coordinate.Group); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Coordinate.RackIndex, b.Coordinate.RackIndex); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Coordinate.NodeIndex, b.Coordinate.NodeIndex); order != 0 {
		return order
	}
	return cmp.Compare(string(a.Node.UID), string(b.Node.UID))
}
