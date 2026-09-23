// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package inventory

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

type capacityRevision uint64

type capacityDecision uint8

const (
	capacityAdmissionRejected capacityDecision = iota
	capacityAdmissionPending
	capacityAdmissionAccepted
)

type capacityAdmissionSnapshot struct {
	revision            capacityRevision
	admitted            []admissionInventory
	rackGroupAdmissions map[types.UID]admissionInventory
	trigger             admissionInventory
	err                 error
}

// CapacityAdmission caches the outcome of admitInventories per input revision
// so concurrent workers share one deterministic decision. Its published slice
// contains one entry per positive-capacity admitted Inventory, bounded by the
// rack limit. One deterministic candidate is retained as a recomputation
// trigger without retaining the rejected set.
type CapacityAdmission struct {
	cache Cache

	revision     atomic.Uint64
	computations atomic.Uint64
	mu           sync.Mutex
	snapshot     *capacityAdmissionSnapshot
	transitions  map[inventoryInstance]admissionInventory
}

// NewCapacityAdmission constructs aggregate admission over informer state.
func NewCapacityAdmission(cache Cache) *CapacityAdmission {
	return &CapacityAdmission{cache: cache}
}

// Invalidate prevents workers using the previous Inventory/Profile snapshot
// from continuing rack-proportional work.
func (a *CapacityAdmission) Invalidate() {
	a.revision.Add(1)
}

func (a *CapacityAdmission) currentRevision() capacityRevision {
	return capacityRevision(a.revision.Load())
}

func (a *CapacityAdmission) current(revision capacityRevision) bool {
	return revision == a.currentRevision()
}

func (a *CapacityAdmission) admits(
	revision capacityRevision,
	inventory *mokkav1alpha1.SGPUInventory,
	capacity DeclaredCapacity,
) (bool, error) {
	decision, err := a.decision(revision, inventory, capacity)
	return decision == capacityAdmissionAccepted, err
}

func (a *CapacityAdmission) decision(
	revision capacityRevision,
	inventory *mokkav1alpha1.SGPUInventory,
	capacity DeclaredCapacity,
) (capacityDecision, error) {
	if !a.current(revision) {
		return capacityAdmissionRejected, errAllocationInputChanged
	}
	snapshot := a.snapshotFor(revision)
	if snapshot.err != nil {
		return capacityAdmissionRejected, snapshot.err
	}
	if !a.current(revision) {
		return capacityAdmissionRejected, errAllocationInputChanged
	}
	if capacity.Racks == 0 {
		if _, admitted := snapshot.rackGroupAdmissions[inventory.UID]; !admitted {
			return capacityAdmissionRejected, nil
		}
		return capacityAdmissionAccepted, nil
	}
	candidate := admissionInventory{
		instance: inventoryInstance{name: inventory.Name, uid: inventory.UID},
		created:  inventory.CreationTimestamp,
		capacity: capacity,
	}
	index, found := slices.BinarySearchFunc(snapshot.admitted, candidate, compareAdmissionInventories)
	if !found {
		return capacityAdmissionRejected, nil
	}
	admitted := snapshot.admitted[index]
	if admitted.capacity != candidate.capacity {
		return capacityAdmissionRejected, errAllocationInputChanged
	}
	if !admitted.ready {
		return capacityAdmissionPending, nil
	}
	return capacityAdmissionAccepted, nil
}

func (a *CapacityAdmission) waiters() []inventoryInstance {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapshot == nil {
		return nil
	}
	waiters := make([]inventoryInstance, 0, len(a.snapshot.admitted))
	for _, candidate := range a.snapshot.admitted {
		if !candidate.ready {
			waiters = append(waiters, candidate.instance)
		}
	}
	return waiters
}

func (a *CapacityAdmission) wakeup() inventoryInstance {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapshot == nil {
		return inventoryInstance{}
	}
	return a.snapshot.trigger.instance
}

func (a *CapacityAdmission) takeTransitions() []inventoryInstance {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.transitions) == 0 {
		return nil
	}
	candidates := make([]admissionInventory, 0, len(a.transitions))
	for _, candidate := range a.transitions {
		candidates = append(candidates, candidate)
	}
	slices.SortFunc(candidates, compareAdmissionInventories)
	transitions := make([]inventoryInstance, len(candidates))
	for index := range candidates {
		transitions[index] = candidates[index].instance
	}
	clear(a.transitions)
	return transitions
}

func (a *CapacityAdmission) snapshotFor(revision capacityRevision) *capacityAdmissionSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapshot != nil && a.snapshot.revision == revision {
		return a.snapshot
	}
	if !a.current(revision) {
		return &capacityAdmissionSnapshot{revision: revision, err: errAllocationInputChanged}
	}
	snapshot := a.computeSnapshot(revision)
	if !a.current(revision) {
		return &capacityAdmissionSnapshot{revision: revision, err: errAllocationInputChanged}
	}
	if snapshot.err == nil {
		a.computations.Add(1)
		if a.snapshot != nil && a.snapshot.err == nil {
			a.recordTransitions(a.snapshot, snapshot)
		}
	}
	a.snapshot = snapshot
	return snapshot
}

//nolint:cyclop // The linear merge must distinguish additions, removals, and readiness changes.
func (a *CapacityAdmission) recordTransitions(previous, current *capacityAdmissionSnapshot) {
	for uid, candidate := range previous.rackGroupAdmissions {
		if _, admitted := current.rackGroupAdmissions[uid]; !admitted {
			a.addTransition(candidate)
		}
	}
	for uid, candidate := range current.rackGroupAdmissions {
		if _, admitted := previous.rackGroupAdmissions[uid]; !admitted {
			a.addTransition(candidate)
		}
	}

	oldIndex, newIndex := 0, 0
	for oldIndex < len(previous.admitted) && newIndex < len(current.admitted) {
		old, next := previous.admitted[oldIndex], current.admitted[newIndex]
		switch order := compareAdmissionInventories(old, next); {
		case order < 0:
			a.addTransition(old)
			oldIndex++
		case order > 0:
			a.addTransition(next)
			newIndex++
		default:
			if old.ready != next.ready {
				a.addTransition(next)
			}
			oldIndex++
			newIndex++
		}
	}
	for ; oldIndex < len(previous.admitted); oldIndex++ {
		a.addTransition(previous.admitted[oldIndex])
	}
	for ; newIndex < len(current.admitted); newIndex++ {
		a.addTransition(current.admitted[newIndex])
	}
}

func (a *CapacityAdmission) addTransition(candidate admissionInventory) {
	if a.transitions == nil {
		a.transitions = make(map[inventoryInstance]admissionInventory)
	}
	a.transitions[candidate.instance] = candidate
}

func (a *CapacityAdmission) computeSnapshot(revision capacityRevision) *capacityAdmissionSnapshot {
	inventories, err := a.cache.Inventories()
	if err != nil {
		return &capacityAdmissionSnapshot{
			revision: revision,
			err:      fmt.Errorf("list inventories for capacity admission: %w", err),
		}
	}
	racks, err := a.cache.Racks()
	if err != nil {
		return &capacityAdmissionSnapshot{
			revision: revision,
			err:      fmt.Errorf("list racks for capacity admission: %w", err),
		}
	}
	durable, liveCapacity, err := durableRackCapacities(racks)
	if err != nil {
		return &capacityAdmissionSnapshot{revision: revision, err: err}
	}

	slices.SortFunc(inventories, compareInventoryAdmissionOrder)
	rackGroupInventories := AdmittedRackGroupInventoryUIDs(inventories)
	rackGroupAdmissions := make(map[types.UID]admissionInventory, len(rackGroupInventories))
	for _, inventory := range inventories {
		if _, admitted := rackGroupInventories[inventory.UID]; admitted {
			rackGroupAdmissions[inventory.UID] = admissionInventory{
				instance: inventoryInstance{name: inventory.Name, uid: inventory.UID},
				created:  inventory.CreationTimestamp,
			}
		}
	}
	candidates, fixedCapacity, err := a.admissionCandidates(
		inventories,
		rackGroupInventories,
		durable,
		liveCapacity,
	)
	if err != nil {
		return &capacityAdmissionSnapshot{revision: revision, err: err}
	}
	admitted := admitInventories(candidates, fixedCapacity, liveCapacity)
	snapshot := &capacityAdmissionSnapshot{
		revision:            revision,
		admitted:            admitted,
		rackGroupAdmissions: rackGroupAdmissions,
	}
	if len(candidates) > 0 {
		snapshot.trigger = candidates[0]
	}
	return snapshot
}

func (a *CapacityAdmission) admissionCandidates(
	inventories []*mokkav1alpha1.SGPUInventory,
	rackGroupInventories map[types.UID]struct{},
	durable map[inventoryInstance]durableInventoryCapacity,
	liveCapacity DeclaredCapacity,
) ([]admissionInventory, DeclaredCapacity, error) {
	candidates := make([]admissionInventory, 0, min(len(inventories), int(MaxInventoryNodes)))
	fixedCapacity := liveCapacity
	for _, inventory := range inventories {
		if _, admitted := rackGroupInventories[inventory.UID]; !admitted {
			continue
		}
		instance := inventoryInstance{name: inventory.Name, uid: inventory.UID}
		candidate, materializes, err := a.admissionCandidate(inventory, durable[instance])
		if err != nil {
			return nil, DeclaredCapacity{}, err
		}
		if !materializes {
			continue
		}
		fixedCapacity, err = subtractCapacity(fixedCapacity, durable[instance].total)
		if err != nil {
			return nil, DeclaredCapacity{}, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, fixedCapacity, nil
}

func (a *CapacityAdmission) admissionCandidate(
	inventory *mokkav1alpha1.SGPUInventory,
	actual durableInventoryCapacity,
) (admissionInventory, bool, error) {
	resolved, issues, err := inventoryMaterialization(a.cache, inventory)
	if err != nil || len(resolved) == 0 {
		return admissionInventory{}, false, err
	}
	desired, err := capacityForResolvedGroups(resolved)
	if err != nil {
		return admissionInventory{}, false, err
	}
	target, err := capacityWithPreservedGroups(desired, issues, actual.groups)
	if err != nil {
		return admissionInventory{}, false, err
	}
	return admissionInventory{
		instance: inventoryInstance{name: inventory.Name, uid: inventory.UID},
		created:  inventory.CreationTimestamp, capacity: desired, targetCapacity: target,
		growth: positiveCapacityDifference(target, actual.total),
	}, true, nil
}

func materializedInventoryCapacity(
	cache Cache,
	inventory *mokkav1alpha1.SGPUInventory,
) (DeclaredCapacity, bool, error) {
	resolved, err := materializedInventoryGroups(cache, inventory)
	if err != nil {
		return DeclaredCapacity{}, false, err
	}
	total, err := capacityForResolvedGroups(resolved)
	return total, len(resolved) > 0, err
}

func materializedInventoryGroups(
	cache Cache,
	inventory *mokkav1alpha1.SGPUInventory,
) ([]resolvedGroup, error) {
	resolved, _, err := inventoryMaterialization(cache, inventory)
	return resolved, err
}

func inventoryMaterialization(
	cache Cache,
	inventory *mokkav1alpha1.SGPUInventory,
) ([]resolvedGroup, []ProfileIssue, error) {
	if inventory == nil || inventory.DeletionTimestamp != nil || validateInventory(inventory) != nil ||
		validateInventoryRackCapacity(inventory) != nil {
		return nil, nil, nil
	}
	resolved, issues, err := (&Reconciler{cache: cache}).resolveGroups(inventory)
	if err != nil {
		return nil, nil, err
	}
	if validateResolvedCapacity(resolved) != nil {
		return nil, nil, nil
	}
	resolved, materializationIssues := validateGroupMaterialization(inventory, resolved)
	return resolved, append(issues, materializationIssues...), nil
}
