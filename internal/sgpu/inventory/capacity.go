// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package inventory

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpurelease "github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
)

const (
	// MaxInventoryNodes is the largest admitted controller topology.
	MaxInventoryNodes int64 = 100_000
	// MaxRackGroups bounds selector classification across all admitted Inventories.
	MaxRackGroups = 64
	// ReasonCapacityExceeded identifies declarations outside the supported topology envelope.
	ReasonCapacityExceeded = "CapacityExceeded"
)

type admissionInventory struct {
	instance       inventoryInstance
	created        metav1.Time
	capacity       DeclaredCapacity
	targetCapacity DeclaredCapacity
	growth         DeclaredCapacity
	ready          bool
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

// admitInventories applies the aggregate capacity rule to candidates ordered
// oldest first. Each candidate is admitted whole when its eventual topology
// fits beside the fixed capacity and every candidate admitted before it.
// Growth then proceeds in the same order: the first growth that does not fit
// the live topology holds back every later one, so readiness never overtakes
// admission order.
func admitInventories(candidates []admissionInventory, fixed, live DeclaredCapacity) []admissionInventory {
	admitted := make([]admissionInventory, 0, min(len(candidates), int(MaxInventoryNodes)))
	targetTotal := fixed
	for _, candidate := range candidates {
		next, err := AddCapacity(targetTotal, candidate.targetCapacity)
		if err != nil || ValidateSupportedCapacity(next) != nil {
			continue
		}
		admitted = append(admitted, candidate)
		targetTotal = next
	}
	livePeak := live
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
	return admitted
}

// orderGroupsForCapacityRelease orders groups that grow after those that do
// not, and marks the groups that grow. An Inventory releases capacity before
// it claims more, because admission charges existing racks until they are
// released: while a cleanup that frees capacity is pending, reconciliation
// stops before a growing group or a rack that would grow. Reconciling the
// other groups first makes every release in a pass known before any growth is
// considered.
func orderGroupsForCapacityRelease(
	groups []resolvedGroup,
	racks []*mokkav1alpha1.SGPURack,
) ([]resolvedGroup, map[string]bool, error) {
	actual := make(map[string]DeclaredCapacity)
	for _, rack := range racks {
		group := rack.Spec.Identity.RackGroup
		var err error
		actual[group], err = AddCapacity(actual[group], capacityForRackSpec(&rack.Spec))
		if err != nil {
			return nil, nil, err
		}
	}
	ordered := slices.Clone(groups)
	growing := make(map[string]bool, len(groups))
	for _, group := range groups {
		desired, err := CapacityForGroup(group.group, group.profile)
		if err != nil {
			return nil, nil, err
		}
		growing[group.group.ID] = !capacityIsZero(positiveCapacityDifference(desired, actual[group.group.ID]))
	}
	slices.SortStableFunc(ordered, func(a, b resolvedGroup) int {
		aGrowing := growing[a.group.ID]
		bGrowing := growing[b.group.ID]
		switch {
		case aGrowing == bGrowing:
			return 0
		case aGrowing:
			return 1
		default:
			return -1
		}
	})
	return ordered, growing, nil
}

// durableCapacityCleanupPending reports whether any cleanup frees capacity,
// which holds back growth until it completes.
func durableCapacityCleanupPending(cleanup []sgpurelease.Cleanup) bool {
	return slices.ContainsFunc(cleanup, func(needed sgpurelease.Cleanup) bool {
		return needed.Reason.FreesCapacity()
	})
}

func rackCapacityGrows(
	existing *mokkav1alpha1.SGPURack,
	target *mokkav1alpha1.SGPURackSpec,
) bool {
	currentCapacity := DeclaredCapacity{}
	if existing != nil {
		currentCapacity = capacityForRackSpec(&existing.Spec)
	}
	return !capacityIsZero(positiveCapacityDifference(capacityForRackSpec(target), currentCapacity))
}

type durableInventoryCapacity struct {
	total  DeclaredCapacity
	groups map[string]DeclaredCapacity
}

// durableRackCapacities charges every existing rack to its Inventory. A rack
// stays charged until informer deletion or shrink observes that the durable
// topology has actually released its capacity.
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
		capacity := capacityForRackSpec(&rack.Spec)
		var err error
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
	if rack == nil || !rack.OwnerMatchesInventoryRef() {
		return inventoryInstance{}, false
	}
	return inventoryInstance{name: rack.Spec.InventoryRef.Name, uid: rack.Spec.InventoryRef.UID}, true
}

// capacityForRackSpec counts what a rack has already rendered. Unlike declared
// capacity, these counts are bounded by in-memory slices and cannot overflow.
func capacityForRackSpec(spec *mokkav1alpha1.SGPURackSpec) DeclaredCapacity {
	return DeclaredCapacity{
		Racks: 1,
		Nodes: int64(len(spec.Nodes)),
		GPUs:  int64(spec.GPUCount()),
	}
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

// compareAdmissionInventories orders candidates oldest first by creation
// timestamp, then name, then UID, so every worker admits in the same order.
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
