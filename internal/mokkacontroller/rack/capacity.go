// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
)

const (
	// MaxInventoryNodes is the largest admitted controller topology.
	MaxInventoryNodes int64 = 100_000
	// MaxRackGroups bounds selector classification across all admitted Inventories.
	MaxRackGroups = 64
	// ReasonCapacityExceeded identifies declarations outside the supported topology envelope.
	ReasonCapacityExceeded = "CapacityExceeded"
)

type capacityRevision uint64

type capacityDecision uint8

const (
	capacityAdmissionRejected capacityDecision = iota
	capacityAdmissionPending
	capacityAdmissionAccepted
)

type admissionInventory struct {
	instance       inventoryInstance
	created        metav1.Time
	capacity       DeclaredCapacity
	targetCapacity DeclaredCapacity
	growth         DeclaredCapacity
	ready          bool
}

type capacityAdmissionSnapshot struct {
	revision            capacityRevision
	admitted            []admissionInventory
	rackGroupAdmissions map[types.UID]admissionInventory
	trigger             admissionInventory
	err                 error
}

// CapacityAdmission coalesces deterministic admission across concurrent
// workers. Its published slice contains one entry per positive-capacity
// admitted Inventory, bounded by the rack limit. One deterministic candidate
// is retained as a recomputation trigger without retaining the rejected set.
// Candidates are considered by creation timestamp, name, and UID; each valid
// materializable Inventory is admitted whole when its eventual topology fits.
// Existing racks remain charged until informer deletion or shrink observes
// that the durable topology has actually released their capacity.
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
	admitted := admitCapacityCandidates(fixedCapacity, candidates)
	prepareCapacityCandidates(liveCapacity, admitted)
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

func capacityWithPreservedGroups(
	desired DeclaredCapacity,
	issues []ProfileIssue,
	actual map[string]DeclaredCapacity,
) (DeclaredCapacity, error) {
	target := desired
	preservedGroups := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if _, seen := preservedGroups[issue.RackGroup]; seen {
			continue
		}
		preservedGroups[issue.RackGroup] = struct{}{}
		var err error
		target, err = AddCapacity(target, actual[issue.RackGroup])
		if err != nil {
			return DeclaredCapacity{}, err
		}
	}
	return target, nil
}

func admitCapacityCandidates(
	fixedCapacity DeclaredCapacity,
	candidates []admissionInventory,
) []admissionInventory {
	admitted := make([]admissionInventory, 0, min(len(candidates), int(MaxInventoryNodes)))
	targetTotal := fixedCapacity
	for _, candidate := range candidates {
		next, err := AddCapacity(targetTotal, candidate.targetCapacity)
		if err != nil || ValidateSupportedCapacity(next) != nil {
			continue
		}
		admitted = append(admitted, candidate)
		targetTotal = next
	}
	return admitted
}

func prepareCapacityCandidates(
	liveCapacity DeclaredCapacity,
	admitted []admissionInventory,
) {
	livePeak := liveCapacity
	growthBlocked := false
	for index := range admitted {
		candidate := &admitted[index]
		if capacityIsZero(candidate.growth) {
			candidate.ready = true
			continue
		}
		if !growthBlocked {
			next, err := AddCapacity(livePeak, candidate.growth)
			if err == nil && ValidateSupportedCapacity(next) == nil {
				candidate.ready = true
				livePeak = next
				continue
			}
			growthBlocked = true
		}
	}
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

type durableInventoryCapacity struct {
	total  DeclaredCapacity
	groups map[string]DeclaredCapacity
}

func durableRackCapacities(
	racks []*mokkav1alpha1.SGPURack,
) (map[inventoryInstance]durableInventoryCapacity, DeclaredCapacity, error) {
	byInventory := make(map[inventoryInstance]durableInventoryCapacity)
	total := DeclaredCapacity{}
	for _, rack := range racks {
		instance, owned := durableRackOwner(rack)
		if !owned {
			continue
		}
		capacity, err := capacityForRack(rack)
		if err != nil {
			return nil, DeclaredCapacity{}, err
		}
		total, err = AddCapacity(total, capacity)
		if err != nil {
			return nil, DeclaredCapacity{}, err
		}
		inventoryCapacity := byInventory[instance]
		if inventoryCapacity.groups == nil {
			inventoryCapacity.groups = make(map[string]DeclaredCapacity)
		}
		inventoryCapacity.total, err = AddCapacity(inventoryCapacity.total, capacity)
		if err != nil {
			return nil, DeclaredCapacity{}, err
		}
		group := rack.Spec.Identity.RackGroup
		inventoryCapacity.groups[group], err = AddCapacity(inventoryCapacity.groups[group], capacity)
		if err != nil {
			return nil, DeclaredCapacity{}, err
		}
		byInventory[instance] = inventoryCapacity
	}
	return byInventory, total, nil
}

func durableRackOwner(rack *mokkav1alpha1.SGPURack) (inventoryInstance, bool) {
	if rack == nil {
		return inventoryInstance{}, false
	}
	owner := controllerInventoryOwner(rack)
	if owner == nil || owner.Name != rack.Spec.InventoryRef.Name || owner.UID != rack.Spec.InventoryRef.UID {
		return inventoryInstance{}, false
	}
	return inventoryInstance{name: owner.Name, uid: owner.UID}, true
}

func capacityForRack(rack *mokkav1alpha1.SGPURack) (DeclaredCapacity, error) {
	capacity, err := capacityForRackSpec(&rack.Spec)
	if err != nil {
		return DeclaredCapacity{}, fmt.Errorf("rack %q: %w", rack.Name, err)
	}
	return capacity, nil
}

func capacityForRackSpec(spec *mokkav1alpha1.SGPURackSpec) (DeclaredCapacity, error) {
	capacity := DeclaredCapacity{Racks: 1, Nodes: int64(len(spec.Nodes))}
	for _, node := range spec.Nodes {
		gpus, ok := checkedAdd(capacity.GPUs, int64(len(node.GPUs)))
		if !ok {
			return DeclaredCapacity{}, errors.New("GPU capacity overflows int64")
		}
		capacity.GPUs = gpus
	}
	return capacity, nil
}

func subtractCapacity(total, remove DeclaredCapacity) (DeclaredCapacity, error) {
	if remove.Racks > total.Racks || remove.Nodes > total.Nodes || remove.GPUs > total.GPUs {
		return DeclaredCapacity{}, errors.New("durable capacity accounting underflow")
	}
	return DeclaredCapacity{
		Racks: total.Racks - remove.Racks,
		Nodes: total.Nodes - remove.Nodes,
		GPUs:  total.GPUs - remove.GPUs,
	}, nil
}

func positiveCapacityDifference(desired, current DeclaredCapacity) DeclaredCapacity {
	return DeclaredCapacity{
		Racks: max(0, desired.Racks-current.Racks),
		Nodes: max(0, desired.Nodes-current.Nodes),
		GPUs:  max(0, desired.GPUs-current.GPUs),
	}
}

func capacityIsZero(capacity DeclaredCapacity) bool {
	return capacity == (DeclaredCapacity{})
}

func compareInventoryAdmissionOrder(a, b *mokkav1alpha1.SGPUInventory) int {
	return compareAdmissionInventories(
		admissionInventory{instance: inventoryInstance{name: a.Name, uid: a.UID}, created: a.CreationTimestamp},
		admissionInventory{instance: inventoryInstance{name: b.Name, uid: b.UID}, created: b.CreationTimestamp},
	)
}

// AdmittedRackGroupInventoryUIDs returns the Inventories whose complete
// declarations fit the global selector budget. Declared groups consume the
// budget before profile resolution so broken references cannot bypass the
// bound and make Node event routing scan an unbounded selector set.
func AdmittedRackGroupInventoryUIDs(
	inventories []*mokkav1alpha1.SGPUInventory,
) map[types.UID]struct{} {
	ordered := make([]*mokkav1alpha1.SGPUInventory, 0, len(inventories))
	for _, inventory := range inventories {
		if inventory != nil {
			ordered = append(ordered, inventory)
		}
	}
	slices.SortFunc(ordered, compareInventoryAdmissionOrder)
	admitted := make(map[types.UID]struct{}, min(len(ordered), MaxRackGroups))
	groups := 0
	for _, inventory := range ordered {
		if inventory.UID == "" || inventory.DeletionTimestamp != nil {
			continue
		}
		declared := len(inventory.Spec.RackGroups)
		if declared == 0 || declared > MaxRackGroups || groups+declared > MaxRackGroups {
			continue
		}
		admitted[inventory.UID] = struct{}{}
		groups += declared
	}
	return admitted
}

func compareAdmissionInventories(a, b admissionInventory) int {
	if order := a.created.Time.Compare(b.created.Time); order != 0 {
		return order
	}
	if order := cmp.Compare(a.instance.name, b.instance.name); order != 0 {
		return order
	}
	return cmp.Compare(string(a.instance.uid), string(b.instance.uid))
}

func aggregateCapacityAdmissionError(inventory *mokkav1alpha1.SGPUInventory) string {
	return fmt.Sprintf(
		"inventory %q is outside the aggregate limit of %d Nodes or racks and %d rack groups; Inventories are admitted whole by oldest-first fit",
		inventory.Name,
		MaxInventoryNodes,
		MaxRackGroups,
	)
}

// DeclaredCapacity holds checked capacity values before conversion to API status types.
type DeclaredCapacity struct {
	Racks int64
	Nodes int64
	GPUs  int64
}

func validateInventoryRackCapacity(inventory *mokkav1alpha1.SGPUInventory) error {
	// Reject aggregate-invalid declarations before profile resolution or any
	// work proportional to the declared rack count.
	total := DeclaredCapacity{}
	for _, group := range inventory.Spec.RackGroups {
		var err error
		total, err = AddCapacity(total, DeclaredCapacity{Racks: int64(group.Count)})
		if err != nil {
			return err
		}
	}
	return ValidateSupportedCapacity(total)
}

// CapacityForGroup computes one group's declared capacity with checked intermediates.
func CapacityForGroup(group mokkav1alpha1.RackGroup, profile *mokkav1alpha1.SGPURackProfile) (DeclaredCapacity, error) {
	if profile == nil {
		return DeclaredCapacity{}, fmt.Errorf("rack group %q profile must not be nil", group.ID)
	}
	racks := int64(group.Count)
	nodes, ok := checkedMultiply(racks, int64(profile.Spec.Rack.NodesPerRack))
	if !ok {
		return DeclaredCapacity{}, fmt.Errorf("rack group %q Node capacity overflows int64", group.ID)
	}
	gpus, ok := checkedMultiply(nodes, int64(profile.Spec.Node.GPUs.Count))
	if !ok {
		return DeclaredCapacity{}, fmt.Errorf("rack group %q GPU capacity overflows int64", group.ID)
	}
	return DeclaredCapacity{Racks: racks, Nodes: nodes, GPUs: gpus}, nil
}

// AddCapacity combines checked group or inventory capacity values.
func AddCapacity(a, b DeclaredCapacity) (DeclaredCapacity, error) {
	racks, ok := checkedAdd(a.Racks, b.Racks)
	if !ok {
		return DeclaredCapacity{}, errors.New("aggregate rack capacity overflows int64")
	}
	nodes, ok := checkedAdd(a.Nodes, b.Nodes)
	if !ok {
		return DeclaredCapacity{}, errors.New("aggregate Node capacity overflows int64")
	}
	gpus, ok := checkedAdd(a.GPUs, b.GPUs)
	if !ok {
		return DeclaredCapacity{}, errors.New("aggregate GPU capacity overflows int64")
	}
	return DeclaredCapacity{Racks: racks, Nodes: nodes, GPUs: gpus}, nil
}

// ValidateSupportedCapacity enforces the controller scale contract and status bounds.
func ValidateSupportedCapacity(capacity DeclaredCapacity) error {
	if capacity.Racks < 0 || capacity.Nodes < 0 || capacity.GPUs < 0 {
		return errors.New("declared capacity must not be negative")
	}
	if capacity.Nodes > MaxInventoryNodes {
		return fmt.Errorf(
			"desired Nodes %d exceed supported maximum %d",
			capacity.Nodes,
			MaxInventoryNodes,
		)
	}
	if capacity.Racks > MaxInventoryNodes {
		return fmt.Errorf("desired racks %d exceed supported maximum %d", capacity.Racks, MaxInventoryNodes)
	}
	if capacity.Racks > math.MaxInt32 || capacity.Nodes > math.MaxInt32 || capacity.GPUs > math.MaxInt32 {
		return errors.New("declared capacity exceeds int32 status bounds")
	}
	return nil
}

// StatusCapacity converts capacity only after the supported bounds are established.
func StatusCapacity(capacity DeclaredCapacity) (mokkav1alpha1.InventoryCapacity, error) {
	if err := ValidateSupportedCapacity(capacity); err != nil {
		return mokkav1alpha1.InventoryCapacity{}, err
	}
	return mokkav1alpha1.InventoryCapacity{
		Racks: int32(capacity.Racks),
		Nodes: int32(capacity.Nodes),
		GPUs:  int32(capacity.GPUs),
	}, nil
}

func checkedMultiply(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, false
	}
	return a * b, true
}

func checkedAdd(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}
