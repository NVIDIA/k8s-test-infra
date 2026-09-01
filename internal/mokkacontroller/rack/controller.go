// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package rack reconciles inventories into durable rack resources using
// informer snapshots. Node metadata projection is deliberately a separate
// controller concern.
package rack

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/materialize"
)

//nolint:revive // These constants define one rack ownership metadata protocol.
const (
	// InventoryFinalizer gates inventory deletion on exact projection cleanup.
	InventoryFinalizer = "mokka.nvidia.com/inventory-cleanup"
	RackFinalizer      = "mokka.nvidia.com/rack-cleanup"
	RackFieldManager   = "mokka-controller"

	InventoryNameLabel     = "mokka.nvidia.com/inventory"
	RackGroupLabel         = "mokka.nvidia.com/rack-group"
	RackIndexLabel         = "mokka.nvidia.com/rack-index"
	InventoryUIDAnnotation = "mokka.nvidia.com/inventory-uid"
)

// InventoryMutations is the narrow live-client surface used for finalizers.
type InventoryMutations interface {
	Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPUInventory, error)
	Update(context.Context, *mokkav1alpha1.SGPUInventory, metav1.UpdateOptions) (*mokkav1alpha1.SGPUInventory, error)
}

// Mutations is the narrow live-client surface used for rack lifecycle operations.
type Mutations interface {
	Create(context.Context, *mokkav1alpha1.SGPURack, metav1.CreateOptions) (*mokkav1alpha1.SGPURack, error)
	Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error)
	List(context.Context, metav1.ListOptions) (*mokkav1alpha1.SGPURackList, error)
	Update(context.Context, *mokkav1alpha1.SGPURack, metav1.UpdateOptions) (*mokkav1alpha1.SGPURack, error)
	Delete(context.Context, string, metav1.DeleteOptions) error
}

// ErrRackCacheStale requests a rate-limited retry after a live rack missing
// from the informer cache has been retired.
var ErrRackCacheStale = errors.New("owned rack is missing from the informer cache")

// CleanupReason identifies why a durable binding must be removed.
type CleanupReason string

//nolint:revive // Cleanup reasons form one closed rack lifecycle vocabulary.
const (
	// CleanupCapacityShrink removes bindings outside the newly declared capacity.
	CleanupCapacityShrink CleanupReason = "CapacityShrink"
	// CleanupCapacityRejected retires an Inventory displaced by aggregate admission.
	CleanupCapacityRejected  CleanupReason = "CapacityRejected"
	CleanupGroupRemoved      CleanupReason = "GroupRemoved"
	CleanupNodeIneligible    CleanupReason = "NodeIneligible"
	CleanupSelectorMismatch  CleanupReason = "SelectorMismatch"
	CleanupRackDeleting      CleanupReason = "RackDeleting"
	CleanupInventoryDeleting CleanupReason = "InventoryDeleting"
)

// CleanupNeeded is the exact binding whose Node projection must be removed
// before reconciliation may clear or retire its rack coordinate.
type CleanupNeeded struct {
	RackName string
	RackUID  types.UID
	Binding  allocate.Binding
	Reason   CleanupReason
}

// CleanupGate is the acknowledgement seam implemented by Node projection.
// An acknowledgement remains ready until the rack cache observes that the
// exact binding is gone, so stale reconciles cannot restore a cleaned binding.
type CleanupGate interface {
	Ready(CleanupNeeded) bool
}

// CleanupGateFunc adapts a function to CleanupGate.
type CleanupGateFunc func(CleanupNeeded) bool

// Ready delegates an exact cleanup query to the wrapped function.
func (f CleanupGateFunc) Ready(cleanup CleanupNeeded) bool { return f(cleanup) }

// ProfileIssue identifies an unresolved or invalid group profile.
type ProfileIssue struct {
	RackGroup   string
	ProfileName string
	Reason      string
}

type profileMaterializationError struct {
	RackName string
	Cause    error
}

func (e *profileMaterializationError) Error() string {
	return fmt.Sprintf("rack %q was rejected by API validation", e.RackName)
}

func (e *profileMaterializationError) Unwrap() error { return e.Cause }

// OwnershipConflict identifies a deterministic rack name that cannot be adopted.
type OwnershipConflict struct {
	RackName  string
	RackGroup string
	OwnerUID  types.UID
}

// OwnershipConflictError makes a concurrent ownership change retryable while
// retaining the structured conflict used to report inventory status.
type OwnershipConflictError struct {
	Conflict OwnershipConflict
	Cause    error
}

func (e *OwnershipConflictError) Error() string {
	return fmt.Sprintf("rack %q has controller-owned fields managed elsewhere: %v", e.Conflict.RackName, e.Cause)
}

func (e *OwnershipConflictError) Unwrap() error { return e.Cause }

// Result contains status inputs and explicit cross-controller cleanup work.
// Expected configuration problems are data, not retryable errors.
type Result struct {
	Accepted           bool
	ResolvedRefs       bool
	ValidationReason   string
	ValidationError    string
	ProfileIssues      []ProfileIssue
	OwnershipConflicts []OwnershipConflict
	CleanupNeeded      []CleanupNeeded
	Allocation         allocate.Plan
	// InventoryAllocation carries the inventory status view when Allocation is
	// deliberately restricted to one reconciled group.
	InventoryAllocation *allocate.Plan
	Work                WorkStats
	Changed             bool
}

// WorkStats exposes deterministic operation counts for reconciliation scale
// regressions without depending on wall-clock timing.
type WorkStats struct {
	AllocationsIndexed int64
	BindingsApplied    int64
	RacksReconciled    int64
}

type profileRevisionPrecomputer func(
	*mokkav1alpha1.SGPURackProfile,
) (materialize.PrecomputedProfileRevision, error)

// Reconciler materializes one cached inventory key at a time.
type Reconciler struct {
	cache                     Cache
	inventories               InventoryMutations
	racks                     Mutations
	cleanup                   CleanupGate
	allocation                *AllocationCache
	precomputeProfileRevision profileRevisionPrecomputer
	// refreshAllocation preserves the standalone reconciler contract for
	// callers that do not wire informer invalidations.
	refreshAllocation bool
}

// NewReconciler constructs a keyed rack reconciler.
func NewReconciler(
	cache Cache,
	inventories InventoryMutations,
	racks Mutations,
	cleanup CleanupGate,
) *Reconciler {
	reconciler := NewReconcilerWithAllocationCache(
		cache, inventories, racks, cleanup, NewAllocationCache(cache),
	)
	reconciler.refreshAllocation = true
	return reconciler
}

// NewReconcilerWithAllocationCache constructs a reconciler sharing one global
// allocation computation across all inventory and group workers.
func NewReconcilerWithAllocationCache(
	cache Cache,
	inventories InventoryMutations,
	racks Mutations,
	cleanup CleanupGate,
	allocation *AllocationCache,
) *Reconciler {
	return &Reconciler{
		cache: cache, inventories: inventories, racks: racks, cleanup: cleanup, allocation: allocation,
	}
}

// Reconcile converges one inventory and all of its rack groups.
func (r *Reconciler) Reconcile(ctx context.Context, key string) (Result, error) {
	return r.reconcile(ctx, key, nil)
}

// ReconcileGroup applies one exact allocation group while retaining a global
// allocation snapshot for cross-inventory overlap and duplicate detection.
func (r *Reconciler) ReconcileGroup(ctx context.Context, key allocate.GroupKey) (Result, error) {
	return r.reconcile(ctx, key.InventoryName, &key)
}

//nolint:cyclop // The branches are the explicit inventory and rack lifecycle state machine.
func (r *Reconciler) reconcile(ctx context.Context, key string, requestedGroup *allocate.GroupKey) (Result, error) {
	result := Result{Accepted: true, ResolvedRefs: true}
	if r.refreshAllocation {
		r.allocation.Invalidate()
	}
	allocationRevision := r.allocation.revision()
	inventory, err := r.cache.Inventory(key)
	if apierrors.IsNotFound(err) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("get inventory %q from cache: %w", key, err)
	}
	if inventory.UID == "" {
		result.Accepted = false
		result.ValidationError = "inventory UID must not be empty"
		return result, nil
	}
	if requestedGroup != nil && inventory.UID != requestedGroup.InventoryUID {
		return result, nil
	}

	if inventory.DeletionTimestamp != nil {
		ownedRacks, listErr := r.cache.RacksByInventoryUID(inventory.UID)
		if listErr != nil {
			return result, fmt.Errorf("get racks for inventory %q from cache: %w", key, listErr)
		}
		ownedRacks = filterOwnedRacks(ownedRacks, inventory)
		return r.reconcileInventoryDeletion(ctx, inventory, ownedRacks, result)
	}
	if err := validateInventory(inventory); err != nil {
		result.Accepted = false
		result.ValidationError = err.Error()
		return result, nil
	}
	if err := validateInventoryRackCapacity(inventory); err != nil {
		result.Accepted = false
		result.ValidationReason = ReasonCapacityExceeded
		result.ValidationError = err.Error()
		return result, nil
	}

	resolved, issues, err := r.resolveGroups(inventory)
	if err != nil {
		return result, err
	}
	if err := validateResolvedCapacity(resolved); err != nil {
		result.Accepted = false
		result.ValidationReason = ReasonCapacityExceeded
		result.ValidationError = err.Error()
		return result, nil
	}
	resolved, materializationIssues := validateGroupMaterialization(inventory, resolved)
	issues = append(issues, materializationIssues...)
	result.ProfileIssues = issues
	result.ResolvedRefs = len(issues) == 0
	materializedCapacity, err := capacityForResolvedGroups(resolved)
	if err != nil {
		return result, err
	}
	decision, err := r.allocation.admission.decision(
		allocationRevision.capacity,
		inventory,
		materializedCapacity,
	)
	if err != nil {
		return result, err
	}
	admissionCurrent := func() error {
		if !r.allocation.admission.current(allocationRevision.capacity) {
			return errAllocationInputChanged
		}
		return nil
	}
	if err := admissionCurrent(); err != nil {
		return result, err
	}
	switch decision {
	case capacityAdmissionPending:
		return result, nil
	case capacityAdmissionRejected:
		result.Accepted = false
		result.ValidationReason = ReasonCapacityExceeded
		result.ValidationError = aggregateCapacityAdmissionError(inventory)
		if requestedGroup != nil {
			return result, nil
		}
		return r.retireCapacityRejectedInventory(ctx, inventory, result, admissionCurrent)
	case capacityAdmissionAccepted:
	default:
		return result, fmt.Errorf("unknown capacity admission decision %d", decision)
	}

	var ownedRacks []*mokkav1alpha1.SGPURack
	if requestedGroup == nil {
		ownedRacks, err = r.cache.RacksByInventoryUID(inventory.UID)
		if err != nil {
			return result, fmt.Errorf("get racks for inventory %q from cache: %w", key, err)
		}
		ownedRacks = filterOwnedRacks(ownedRacks, inventory)
	}
	if !slices.Contains(inventory.Finalizers, InventoryFinalizer) {
		if requestedGroup != nil {
			return result, nil
		}
		if err := admissionCurrent(); err != nil {
			return result, err
		}
		changed, err := r.mutateInventory(ctx, inventory, func(latest *mokkav1alpha1.SGPUInventory) bool {
			if slices.Contains(latest.Finalizers, InventoryFinalizer) {
				return false
			}
			latest.Finalizers = append(latest.Finalizers, InventoryFinalizer)
			return true
		})
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || changed
	}
	var workGroups []resolvedGroup
	growingWorkGroups := make(map[string]bool)
	if requestedGroup != nil {
		workGroups = slices.DeleteFunc(slices.Clone(resolved), func(group resolvedGroup) bool {
			return group.key != *requestedGroup
		})
	} else {
		workGroups, growingWorkGroups, err = orderGroupsForCapacityRelease(resolved, ownedRacks)
		if err != nil {
			return result, err
		}
	}

	allocation, err := r.allocation.planRevision(
		allocationRevision,
		requestedGroup,
		inventoryInstance{name: inventory.Name, uid: inventory.UID},
	)
	if err != nil {
		return result, err
	}
	result.Allocation = allocation
	if requestedGroup != nil {
		inventoryAllocation, viewErr := r.allocation.planRevision(
			allocationRevision,
			nil,
			inventoryInstance{name: inventory.Name, uid: inventory.UID},
		)
		if viewErr != nil {
			return result, viewErr
		}
		result.InventoryAllocation = &inventoryAllocation
	}

	resolvedByID := make(map[string]resolvedGroup, len(resolved))
	for _, group := range resolved {
		resolvedByID[group.group.ID] = group
	}
	unresolved := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		unresolved[issue.RackGroup] = struct{}{}
	}

	blockedNames := make(map[string]struct{})
	existingByName := make(map[string]*mokkav1alpha1.SGPURack, len(ownedRacks))
	for _, existing := range ownedRacks {
		if err := admissionCurrent(); err != nil {
			return result, err
		}
		existingByName[existing.Name] = existing
		if existing.DeletionTimestamp == nil {
			continue
		}
		blockedNames[existing.Name] = struct{}{}
		if requestedGroup != nil {
			continue
		}
		changed, cleanup, err := r.retireRack(ctx, inventory, existing, CleanupRackDeleting)
		if err != nil {
			appendOwnershipConflict(&result, err)
			sortResult(&result)
			return result, err
		}
		result.Changed = result.Changed || changed
		result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
	}

	if requestedGroup == nil { //nolint:nestif // Full inventory reconciliation also owns rack retirement.
		for _, existing := range ownedRacks {
			if err := admissionCurrent(); err != nil {
				return result, err
			}
			if existing.DeletionTimestamp != nil {
				continue
			}
			if _, keepLastGood := unresolved[existing.Spec.Identity.RackGroup]; keepLastGood {
				continue
			}
			group, declared := resolvedByID[existing.Spec.Identity.RackGroup]
			if declared && existing.Spec.Identity.RackIndex >= 0 &&
				existing.Spec.Identity.RackIndex < group.group.Count &&
				existing.Name == materialize.RackName(
					inventory.Name,
					inventory.UID,
					group.group.ID,
					existing.Spec.Identity.RackIndex,
				) {
				continue
			}
			reason := CleanupGroupRemoved
			if group, exists := resolvedByID[existing.Spec.Identity.RackGroup]; exists &&
				existing.Spec.Identity.RackIndex >= group.group.Count {
				reason = CleanupCapacityShrink
			}
			changed, cleanup, err := r.retireRack(ctx, inventory, existing, reason)
			if err != nil {
				appendOwnershipConflict(&result, err)
				sortResult(&result)
				return result, err
			}
			result.Changed = result.Changed || changed
			result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
		}
	}

	var allocations allocationIndex
	var changedRackIndices []int32
	if requestedGroup != nil && len(workGroups) == 1 {
		changedRackIndices = allocationChangedRacks(allocation, *requestedGroup, workGroups[0].group.Count)
		allocations = indexGroupAllocation(allocation, *requestedGroup, changedRackIndices)
	} else {
		allocations = indexAllocation(allocation, inventory.UID, resolvedByID)
	}
	result.Work.AllocationsIndexed = allocations.indexed
	for _, group := range workGroups {
		if requestedGroup == nil && growingWorkGroups[group.group.ID] &&
			durableCapacityCleanupPending(result.CleanupNeeded) {
			sortResult(&result)
			return result, nil
		}
		capacityGrowthBlocked := false
		reconcileRack := func(rackIndex int32) error {
			if err := admissionCurrent(); err != nil {
				return err
			}
			result.Work.RacksReconciled++
			rendered, err := materialize.RenderRackWithRevision(materialize.RackInput{
				InventoryName: inventory.Name,
				InventoryUID:  inventory.UID,
				Group:         group.group,
				RackIndex:     rackIndex,
				Profile:       group.profile,
			}, group.revision)
			if err != nil {
				return fmt.Errorf("render rack group %q index %d: %w", group.group.ID, rackIndex, err)
			}
			if _, blocked := blockedNames[rendered.Name]; blocked {
				return nil
			}
			existing := existingByName[rendered.Name]
			if existing == nil {
				cached, err := r.cache.Rack(rendered.Name)
				switch {
				case err == nil:
					existing = cached
				case apierrors.IsNotFound(err):
					if requestedGroup != nil {
						return nil
					}
				default:
					return fmt.Errorf("get rack %q from cache: %w", rendered.Name, err)
				}
			}
			if requestedGroup != nil && existing != nil && existing.DeletionTimestamp != nil {
				return nil
			}
			if existing != nil && !controlledByInventory(existing, inventory) {
				result.OwnershipConflicts = append(result.OwnershipConflicts, ownershipConflict(existing, group.group.ID))
				return nil
			}

			targetSpec := rendered.Spec
			partition := allocations.racks[allocationRackKey{group: group.key, rackIndex: rackIndex}]
			result.Work.BindingsApplied += applyBindings(&targetSpec, partition.retained)
			result.Work.BindingsApplied += applyBindings(&targetSpec, partition.assigned)
			if existing != nil {
				cleanup := r.preservePendingReleases(
					existing,
					&targetSpec,
					allocations.releases,
				)
				result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
			}
			if err := materialize.ValidateRackSpec(targetSpec); err != nil {
				result.ProfileIssues = append(result.ProfileIssues, ProfileIssue{
					RackGroup: group.group.ID, ProfileName: group.profile.Name, Reason: err.Error(),
				})
				result.ResolvedRefs = false
				return nil
			}
			if durableCapacityCleanupPending(result.CleanupNeeded) {
				grows, err := rackCapacityGrows(existing, &targetSpec)
				if err != nil {
					return fmt.Errorf("compute target capacity for rack %q: %w", rendered.Name, err)
				}
				if grows {
					capacityGrowthBlocked = true
					return nil
				}
			}

			if err := admissionCurrent(); err != nil {
				return err
			}
			changed, conflict, err := r.createOrUpdateRack(ctx, inventory, existing, rendered.Name, targetSpec)
			if conflict != nil {
				result.OwnershipConflicts = append(result.OwnershipConflicts, *conflict)
			}
			if err != nil {
				var materializationErr *profileMaterializationError
				if errors.As(err, &materializationErr) {
					result.ProfileIssues = append(result.ProfileIssues, ProfileIssue{
						RackGroup: group.group.ID, ProfileName: group.profile.Name, Reason: materializationErr.Error(),
					})
					result.ResolvedRefs = false
					return nil
				}
				return err
			}
			result.Changed = result.Changed || changed
			return nil
		}

		if requestedGroup != nil {
			for _, rackIndex := range changedRackIndices {
				if err := reconcileRack(rackIndex); err != nil {
					sortResult(&result)
					return result, err
				}
				if !result.ResolvedRefs || capacityGrowthBlocked {
					break
				}
			}
			if capacityGrowthBlocked {
				sortResult(&result)
				return result, nil
			}
			continue
		}
		for rackIndex := int32(0); rackIndex < group.group.Count; rackIndex++ {
			if err := reconcileRack(rackIndex); err != nil {
				sortResult(&result)
				return result, err
			}
			if !result.ResolvedRefs || capacityGrowthBlocked {
				break
			}
		}
		if capacityGrowthBlocked {
			sortResult(&result)
			return result, nil
		}
	}

	sortResult(&result)
	return result, nil
}

type resolvedGroup struct {
	group    mokkav1alpha1.RackGroup
	profile  *mokkav1alpha1.SGPURackProfile
	revision materialize.PrecomputedProfileRevision
	key      allocate.GroupKey
}

func (r *Reconciler) resolveGroups(inventory *mokkav1alpha1.SGPUInventory) ([]resolvedGroup, []ProfileIssue, error) {
	resolved := make([]resolvedGroup, 0, len(inventory.Spec.RackGroups))
	issues := make([]ProfileIssue, 0)
	type profileObservation struct {
		uid        types.UID
		generation int64
	}
	type observedRevision struct {
		profile  *mokkav1alpha1.SGPURackProfile
		revision materialize.PrecomputedProfileRevision
	}
	profiles := make(map[string]*mokkav1alpha1.SGPURackProfile)
	revisions := make(map[profileObservation]observedRevision)
	for _, group := range inventory.Spec.RackGroups {
		profile := profiles[group.ProfileRef.Name]
		if profile == nil {
			var err error
			profile, err = r.cache.Profile(group.ProfileRef.Name)
			if err != nil {
				if apierrors.IsNotFound(err) {
					issues = append(issues, ProfileIssue{RackGroup: group.ID, ProfileName: group.ProfileRef.Name, Reason: "NotFound"})
					continue
				}
				return nil, nil, fmt.Errorf("get profile %q from cache: %w", group.ProfileRef.Name, err)
			}
			profiles[group.ProfileRef.Name] = profile
		}
		if err := materialize.ValidateProfile(profile.Spec); err != nil {
			issues = append(issues, ProfileIssue{RackGroup: group.ID, ProfileName: group.ProfileRef.Name, Reason: err.Error()})
			continue
		}
		observation := profileObservation{uid: profile.UID, generation: profile.Generation}
		observed, found := revisions[observation]
		if found && observed.profile != profile {
			return nil, nil, fmt.Errorf(
				"profile UID %q generation %d has inconsistent cache observations",
				profile.UID,
				profile.Generation,
			)
		}
		if !found {
			revision, err := r.profileRevision(profile)
			if err != nil {
				return nil, nil, fmt.Errorf("compute revision for profile %q: %w", profile.Name, err)
			}
			observed = observedRevision{profile: profile, revision: revision}
			revisions[observation] = observed
		}
		resolved = append(resolved, resolvedGroup{
			group:    group,
			profile:  profile,
			revision: observed.revision,
			key:      allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: group.ID},
		})
	}
	return resolved, issues, nil
}

func (r *Reconciler) profileRevision(
	profile *mokkav1alpha1.SGPURackProfile,
) (materialize.PrecomputedProfileRevision, error) {
	if r.precomputeProfileRevision != nil {
		return r.precomputeProfileRevision(profile)
	}
	return materialize.PrecomputeProfileRevision(profile)
}

func validateResolvedCapacity(groups []resolvedGroup) error {
	total, err := capacityForResolvedGroups(groups)
	if err != nil {
		return err
	}
	return ValidateSupportedCapacity(total)
}

func capacityForResolvedGroups(groups []resolvedGroup) (DeclaredCapacity, error) {
	total := DeclaredCapacity{}
	for _, group := range groups {
		capacity, err := CapacityForGroup(group.group, group.profile)
		if err != nil {
			return DeclaredCapacity{}, err
		}
		if err := ValidateSupportedCapacity(capacity); err != nil {
			return DeclaredCapacity{}, err
		}
		total, err = AddCapacity(total, capacity)
		if err != nil {
			return DeclaredCapacity{}, err
		}
	}
	return total, nil
}

func orderGroupsForCapacityRelease(
	groups []resolvedGroup,
	racks []*mokkav1alpha1.SGPURack,
) ([]resolvedGroup, map[string]bool, error) {
	actual := make(map[string]DeclaredCapacity)
	for _, rack := range racks {
		capacity, err := capacityForRack(rack)
		if err != nil {
			return nil, nil, err
		}
		group := rack.Spec.Identity.RackGroup
		actual[group], err = AddCapacity(actual[group], capacity)
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

func durableCapacityCleanupPending(cleanup []CleanupNeeded) bool {
	for _, needed := range cleanup {
		switch needed.Reason {
		case CleanupCapacityShrink, CleanupGroupRemoved, CleanupRackDeleting:
			return true
		case CleanupCapacityRejected, CleanupNodeIneligible, CleanupSelectorMismatch, CleanupInventoryDeleting:
		}
	}
	return false
}

func rackCapacityGrows(
	existing *mokkav1alpha1.SGPURack,
	target *mokkav1alpha1.SGPURackSpec,
) (bool, error) {
	currentCapacity := DeclaredCapacity{}
	if existing != nil {
		var err error
		currentCapacity, err = capacityForRack(existing)
		if err != nil {
			return false, err
		}
	}
	targetCapacity, err := capacityForRackSpec(target)
	if err != nil {
		return false, err
	}
	return !capacityIsZero(positiveCapacityDifference(targetCapacity, currentCapacity)), nil
}

func validateGroupMaterialization(
	inventory *mokkav1alpha1.SGPUInventory,
	groups []resolvedGroup,
) ([]resolvedGroup, []ProfileIssue) {
	valid := make([]resolvedGroup, 0, len(groups))
	issues := make([]ProfileIssue, 0)
	for _, group := range groups {
		_, err := materialize.RenderRackWithRevision(materialize.RackInput{
			InventoryName: inventory.Name,
			InventoryUID:  inventory.UID,
			Group:         group.group,
			RackIndex:     0,
			Profile:       group.profile,
		}, group.revision)
		if err != nil {
			issues = append(issues, ProfileIssue{
				RackGroup: group.group.ID, ProfileName: group.profile.Name, Reason: err.Error(),
			})
			continue
		}
		valid = append(valid, group)
	}
	return valid, issues
}

func (r *Reconciler) preservePendingReleases(
	existing *mokkav1alpha1.SGPURack,
	target *mokkav1alpha1.SGPURackSpec,
	releases map[allocate.Coordinate]allocate.Release,
) []CleanupNeeded {
	cleanup := make([]CleanupNeeded, 0)
	targetSlots := make(map[int32]*mokkav1alpha1.SGPURackNode, len(target.Nodes))
	for i := range target.Nodes {
		targetSlots[target.Nodes[i].Index] = &target.Nodes[i]
	}
	for _, slot := range existing.Spec.Nodes {
		if slot.NodeRef == nil {
			continue
		}
		coordinate := allocate.Coordinate{
			Group: allocate.GroupKey{
				InventoryName: existing.Spec.InventoryRef.Name,
				InventoryUID:  existing.Spec.InventoryRef.UID,
				RackGroup:     existing.Spec.Identity.RackGroup,
			},
			RackIndex: existing.Spec.Identity.RackIndex, NodeIndex: slot.Index,
		}
		release, found := releases[coordinate]
		if !found {
			continue
		}
		needed := CleanupNeeded{RackName: existing.Name, RackUID: existing.UID, Binding: release.Binding, Reason: cleanupReason(release.Reason)}
		if r.cleanup != nil && r.cleanup.Ready(needed) {
			continue
		}
		cleanup = append(cleanup, needed)
		if targetSlot := targetSlots[slot.Index]; targetSlot != nil {
			targetSlot.NodeRef = slot.NodeRef.DeepCopy()
			continue
		}
		target.Nodes = append(target.Nodes, *slot.DeepCopy())
	}
	slices.SortFunc(target.Nodes, func(a, b mokkav1alpha1.SGPURackNode) int { return cmp.Compare(a.Index, b.Index) })
	return cleanup
}

//nolint:cyclop // Retirement gates deletion on every exact slot cleanup acknowledgement.
func (r *Reconciler) retireRack(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	rack *mokkav1alpha1.SGPURack,
	reason CleanupReason,
) (bool, []CleanupNeeded, error) {
	clearSlots := make(map[int32]types.UID)
	cleanup := make([]CleanupNeeded, 0)
	for _, slot := range rack.Spec.Nodes {
		if slot.NodeRef == nil {
			continue
		}
		binding := allocate.Binding{
			Coordinate: allocate.Coordinate{
				Group:     allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: rack.Spec.Identity.RackGroup},
				RackIndex: rack.Spec.Identity.RackIndex, NodeIndex: slot.Index,
			},
			Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
		}
		needed := CleanupNeeded{RackName: rack.Name, RackUID: rack.UID, Binding: binding, Reason: reason}
		if r.cleanup != nil && r.cleanup.Ready(needed) {
			clearSlots[slot.Index] = slot.NodeRef.UID
			continue
		}
		cleanup = append(cleanup, needed)
	}

	changed, empty, latest, err := r.mutateRack(ctx, inventory, rack, func(latest *mokkav1alpha1.SGPURack) bool {
		mutated := false
		for i := range latest.Spec.Nodes {
			ref := latest.Spec.Nodes[i].NodeRef
			if ref == nil {
				continue
			}
			if uid, shouldClear := clearSlots[latest.Spec.Nodes[i].Index]; shouldClear && uid == ref.UID {
				latest.Spec.Nodes[i].NodeRef = nil
				mutated = true
			}
		}
		empty := rackEmpty(latest)
		if empty && slices.Contains(latest.Finalizers, RackFinalizer) {
			latest.Finalizers = removeString(latest.Finalizers, RackFinalizer)
			mutated = true
		}
		return mutated
	})
	if err != nil {
		return changed, cleanup, err
	}
	if latest != nil {
		empty = rackEmpty(latest)
	}
	if !empty {
		return changed, cleanup, nil
	}
	uid := rack.UID
	rv := rack.ResourceVersion
	if latest != nil {
		uid = latest.UID
		rv = latest.ResourceVersion
	}
	err = r.racks.Delete(ctx, rack.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
	if err != nil && !apierrors.IsNotFound(err) {
		return changed, cleanup, fmt.Errorf("delete rack %q: %w", rack.Name, err)
	}
	return true, cleanup, nil
}

func (r *Reconciler) retireCapacityRejectedInventory(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	result Result,
	admissionCurrent func() error,
) (Result, error) {
	ownedRacks, err := r.cache.RacksByInventoryUID(inventory.UID)
	if err != nil {
		return result, fmt.Errorf("get racks for inventory %q from cache: %w", inventory.Name, err)
	}
	for _, rack := range filterOwnedRacks(ownedRacks, inventory) {
		if err := admissionCurrent(); err != nil {
			return result, err
		}
		changed, cleanup, err := r.retireRack(ctx, inventory, rack, CleanupCapacityRejected)
		if err != nil {
			appendOwnershipConflict(&result, err)
			sortResult(&result)
			return result, err
		}
		result.Changed = result.Changed || changed
		result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
	}
	sortResult(&result)
	return result, nil
}

func (r *Reconciler) reconcileInventoryDeletion(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	racks []*mokkav1alpha1.SGPURack,
	result Result,
) (Result, error) {
	allGone := true
	for _, rack := range racks {
		changed, cleanup, err := r.retireRack(ctx, inventory, rack, CleanupInventoryDeleting)
		if err != nil {
			appendOwnershipConflict(&result, err)
			sortResult(&result)
			return result, err
		}
		result.Changed = result.Changed || changed
		result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
		if len(cleanup) > 0 {
			allGone = false
		}
	}
	if allGone && slices.Contains(inventory.Finalizers, InventoryFinalizer) {
		var err error
		result, err = r.reconcileTerminalInventoryDeletion(ctx, inventory, result)
		sortResult(&result)
		return result, err
	}
	sortResult(&result)
	return result, nil
}

func (r *Reconciler) reconcileTerminalInventoryDeletion(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	result Result,
) (Result, error) {
	liveRacks, err := r.liveOwnedRacks(ctx, inventory)
	if err != nil {
		return result, err
	}
	if len(liveRacks) > 0 {
		return r.retireLiveOwnedRacks(ctx, inventory, liveRacks, result)
	}
	changed, err := r.mutateInventory(ctx, inventory, func(latest *mokkav1alpha1.SGPUInventory) bool {
		if !slices.Contains(latest.Finalizers, InventoryFinalizer) {
			return false
		}
		latest.Finalizers = removeString(latest.Finalizers, InventoryFinalizer)
		return true
	})
	if err != nil {
		return result, err
	}
	result.Changed = result.Changed || changed
	return result, nil
}

func (r *Reconciler) retireLiveOwnedRacks(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	racks []*mokkav1alpha1.SGPURack,
	result Result,
) (Result, error) {
	for _, rack := range racks {
		changed, cleanup, err := r.retireRack(ctx, inventory, rack, CleanupInventoryDeleting)
		if err != nil {
			appendOwnershipConflict(&result, err)
			return result, err
		}
		result.Changed = result.Changed || changed
		result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
	}
	return result, fmt.Errorf("%w for inventory %q", ErrRackCacheStale, inventory.Name)
}

func (r *Reconciler) liveOwnedRacks(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
) ([]*mokkav1alpha1.SGPURack, error) {
	options := metav1.ListOptions{}
	// Controller-created racks always carry this label when the inventory name
	// is a valid value. Fall back to an unscoped list when that invariant cannot
	// hold so the final absence check remains complete.
	if len(validation.IsValidLabelValue(inventory.Name)) == 0 {
		options.LabelSelector = labels.Set{InventoryNameLabel: inventory.Name}.String()
	}
	live, err := r.racks.List(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("list live racks for inventory %q: %w", inventory.Name, err)
	}
	if live == nil {
		return nil, fmt.Errorf("list live racks for inventory %q returned no object", inventory.Name)
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(live.Items))
	for i := range live.Items {
		racks = append(racks, &live.Items[i])
	}
	return filterOwnedRacks(racks, inventory), nil
}

func (r *Reconciler) createOrUpdateRack(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	existing *mokkav1alpha1.SGPURack,
	name string,
	spec mokkav1alpha1.SGPURackSpec,
) (bool, *OwnershipConflict, error) {
	if existing == nil {
		return r.createCacheMissingRack(ctx, inventory, name, spec)
	}
	if !controlledByInventory(existing, inventory) {
		conflict := ownershipConflict(existing, spec.Identity.RackGroup)
		return false, &conflict, nil
	}
	changed, _, _, err := r.mutateRack(ctx, inventory, existing, func(latest *mokkav1alpha1.SGPURack) bool {
		before := latest.DeepCopy()
		latest.Spec = *spec.DeepCopy()
		ensureRackMetadata(latest, inventory)
		return !rackSemanticEqual(before, latest)
	})
	if err != nil {
		var ownershipErr *OwnershipConflictError
		if errors.As(err, &ownershipErr) {
			return changed, &ownershipErr.Conflict, err
		}
	}
	return changed, nil, err
}

func (r *Reconciler) createCacheMissingRack(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	name string,
	spec mokkav1alpha1.SGPURackSpec,
) (bool, *OwnershipConflict, error) {
	desired := newRack(inventory, name, spec)
	created, err := r.racks.Create(ctx, desired, metav1.CreateOptions{FieldManager: RackFieldManager})
	if err == nil {
		if created == nil {
			return false, nil, fmt.Errorf("create rack %q returned no object", name)
		}
		if !controlledByInventory(created, inventory) {
			conflict := ownershipConflict(created, spec.Identity.RackGroup)
			return false, &conflict, &OwnershipConflictError{
				Conflict: conflict, Cause: errors.New("created rack has a different controller owner"),
			}
		}
		return true, nil, nil
	}
	if apierrors.IsInvalid(err) {
		return false, nil, &profileMaterializationError{RackName: desired.Name, Cause: err}
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, nil, fmt.Errorf("create rack %q: %w", name, err)
	}
	created, err = r.racks.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, nil, fmt.Errorf("get rack %q after create reported already exists: %w", name, err)
	}
	return r.createOrUpdateRack(ctx, inventory, created, name, spec)
}

func (r *Reconciler) mutateInventory(
	ctx context.Context,
	base *mokkav1alpha1.SGPUInventory,
	mutate func(*mokkav1alpha1.SGPUInventory) bool,
) (bool, error) {
	latest := base.DeepCopy()
	changed := false
	first := true
	err := retryOnConflict(func() error {
		if !first {
			var err error
			latest, err = r.inventories.Get(ctx, base.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if latest.UID != base.UID {
				return apierrors.NewConflict(mokkav1alpha1.Resource("sgpuinventories"), base.Name, errors.New("inventory UID changed"))
			}
		}
		first = false
		candidate := latest.DeepCopy()
		if !mutate(candidate) {
			return nil
		}
		updated, err := r.inventories.Update(ctx, candidate, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		latest = updated
		changed = true
		return nil
	})
	if err != nil {
		return changed, fmt.Errorf("update inventory %q: %w", base.Name, err)
	}
	return changed, nil
}

//nolint:cyclop // Rack updates validate identity, ownership, and optimistic-concurrency invariants together.
func (r *Reconciler) mutateRack(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	base *mokkav1alpha1.SGPURack,
	mutate func(*mokkav1alpha1.SGPURack) bool,
) (bool, bool, *mokkav1alpha1.SGPURack, error) {
	latest := base.DeepCopy()
	changed := false
	first := true
	err := retryOnConflict(func() error {
		if !first {
			var err error
			latest, err = r.racks.Get(ctx, base.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if latest.UID != base.UID {
				return apierrors.NewConflict(mokkav1alpha1.Resource("sgpuracks"), base.Name, errors.New("rack UID changed"))
			}
			if !equality.Semantic.DeepEqual(latest.Spec, base.Spec) {
				return apierrors.NewConflict(mokkav1alpha1.Resource("sgpuracks"), base.Name, errors.New("rack spec changed concurrently"))
			}
		}
		first = false
		if !controlledByInventory(latest, inventory) {
			conflict := ownershipConflict(latest, base.Spec.Identity.RackGroup)
			return &OwnershipConflictError{Conflict: conflict, Cause: apierrors.NewConflict(
				mokkav1alpha1.Resource("sgpuracks"), base.Name, errors.New("rack ownership changed"),
			)}
		}
		candidate := latest.DeepCopy()
		if !mutate(candidate) {
			return nil
		}
		if !controllerOwnsRackSpec(latest) {
			conflict := ownershipConflict(latest, base.Spec.Identity.RackGroup)
			return &OwnershipConflictError{Conflict: conflict, Cause: apierrors.NewConflict(
				mokkav1alpha1.Resource("sgpuracks"), base.Name, errors.New("rack spec is not exclusively controller-owned"),
			)}
		}
		updated, err := r.updateRack(ctx, candidate)
		if err != nil {
			_, classified := r.classifyRackUpdateError(ctx, inventory, candidate, err)
			return classified
		}
		if updated.UID != base.UID {
			return apierrors.NewConflict(
				mokkav1alpha1.Resource("sgpuracks"), base.Name, errors.New("rack UID changed"),
			)
		}
		latest = updated
		changed = true
		return nil
	})
	if err != nil {
		return changed, false, latest, fmt.Errorf("update rack %q: %w", base.Name, err)
	}
	return changed, rackEmpty(latest), latest, nil
}

func (r *Reconciler) updateRack(ctx context.Context, desired *mokkav1alpha1.SGPURack) (*mokkav1alpha1.SGPURack, error) {
	updated, err := r.racks.Update(ctx, desired, metav1.UpdateOptions{FieldManager: RackFieldManager})
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, fmt.Errorf("update rack %q returned no object", desired.Name)
	}
	return updated, nil
}

func (r *Reconciler) classifyRackUpdateError(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	desired *mokkav1alpha1.SGPURack,
	err error,
) (*OwnershipConflict, error) {
	if apierrors.IsInvalid(err) {
		return nil, &profileMaterializationError{RackName: desired.Name, Cause: err}
	}
	latest, getErr := r.racks.Get(ctx, desired.Name, metav1.GetOptions{})
	if getErr == nil && !controlledByInventory(latest, inventory) {
		conflict := ownershipConflict(latest, desired.Spec.Identity.RackGroup)
		return &conflict, &OwnershipConflictError{Conflict: conflict, Cause: err}
	}
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return nil, fmt.Errorf("check ownership after rack %q update failed: %w", desired.Name, getErr)
	}
	return nil, err
}

//nolint:cyclop // Validation reports the precise independently-invalid inventory field.
func validateInventory(inventory *mokkav1alpha1.SGPUInventory) error {
	if inventory.Name == "" || inventory.UID == "" {
		return errors.New("inventory identity must include name and UID")
	}
	if len(inventory.Spec.RackGroups) > 64 {
		return errors.New("rackGroups must contain at most 64 entries")
	}
	seen := make(map[string]struct{}, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		if errs := validation.IsDNS1123Label(group.ID); len(errs) > 0 {
			return fmt.Errorf("rack group ID %q is invalid: %s", group.ID, errs[0])
		}
		if _, exists := seen[group.ID]; exists {
			return fmt.Errorf("rack group ID %q is duplicated", group.ID)
		}
		seen[group.ID] = struct{}{}
		if group.Count < 1 || int64(group.Count) > MaxInventoryNodes {
			return fmt.Errorf("rack group %q count is outside [1,%d]", group.ID, MaxInventoryNodes)
		}
		if group.ProfileRef.Name == "" {
			return fmt.Errorf("rack group %q profileRef.name must not be empty", group.ID)
		}
		if group.Placement != nil && group.Placement.NodeSelector != nil {
			if err := allocate.ValidatePlacementSelector(group.Placement.NodeSelector); err != nil {
				return fmt.Errorf("rack group %q selector: %w", group.ID, err)
			}
		}
	}
	return nil
}

func newRack(inventory *mokkav1alpha1.SGPUInventory, name string, spec mokkav1alpha1.SGPURackSpec) *mokkav1alpha1.SGPURack {
	rack := &mokkav1alpha1.SGPURack{
		TypeMeta:   metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPURack"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
	ensureRackMetadata(rack, inventory)
	return rack
}

func ensureRackMetadata(rack *mokkav1alpha1.SGPURack, inventory *mokkav1alpha1.SGPUInventory) {
	if rack.Labels == nil {
		rack.Labels = make(map[string]string)
	}
	if len(validation.IsValidLabelValue(inventory.Name)) == 0 {
		rack.Labels[InventoryNameLabel] = inventory.Name
	} else {
		delete(rack.Labels, InventoryNameLabel)
	}
	rack.Labels[RackGroupLabel] = rack.Spec.Identity.RackGroup
	rack.Labels[RackIndexLabel] = strconv.FormatInt(int64(rack.Spec.Identity.RackIndex), 10)
	if rack.Annotations == nil {
		rack.Annotations = make(map[string]string)
	}
	rack.Annotations[InventoryUIDAnnotation] = string(inventory.UID)
	if !slices.Contains(rack.Finalizers, RackFinalizer) {
		rack.Finalizers = append(rack.Finalizers, RackFinalizer)
	}
	owner := metav1.NewControllerRef(inventory, mokkav1alpha1.SchemeGroupVersion.WithKind("SGPUInventory"))
	owner.BlockOwnerDeletion = ptr.To(false)
	if existing := metav1.GetControllerOf(rack); existing == nil {
		rack.OwnerReferences = append(rack.OwnerReferences, *owner)
	}
}

func controlledByInventory(rack *mokkav1alpha1.SGPURack, inventory *mokkav1alpha1.SGPUInventory) bool {
	owner := metav1.GetControllerOf(rack)
	return owner != nil && owner.APIVersion == mokkav1alpha1.SchemeGroupVersion.String() &&
		owner.Kind == "SGPUInventory" && owner.Name == inventory.Name && owner.UID == inventory.UID
}

func controllerInventoryOwner(rack *mokkav1alpha1.SGPURack) *metav1.OwnerReference {
	owner := metav1.GetControllerOf(rack)
	if owner == nil || owner.APIVersion != mokkav1alpha1.SchemeGroupVersion.String() || owner.Kind != "SGPUInventory" {
		return nil
	}
	return owner
}

func filterOwnedRacks(racks []*mokkav1alpha1.SGPURack, inventory *mokkav1alpha1.SGPUInventory) []*mokkav1alpha1.SGPURack {
	filtered := make([]*mokkav1alpha1.SGPURack, 0, len(racks))
	for _, rack := range racks {
		if controlledByInventory(rack, inventory) {
			filtered = append(filtered, rack)
		}
	}
	slices.SortFunc(filtered, func(a, b *mokkav1alpha1.SGPURack) int { return cmp.Compare(a.Name, b.Name) })
	return filtered
}

type allocationRackKey struct {
	group     allocate.GroupKey
	rackIndex int32
}

type rackAllocation struct {
	retained []allocate.Binding
	assigned []allocate.Binding
}

type allocationIndex struct {
	racks    map[allocationRackKey]rackAllocation
	releases map[allocate.Coordinate]allocate.Release
	indexed  int64
}

func indexAllocation(
	plan allocate.Plan,
	inventoryUID types.UID,
	resolved map[string]resolvedGroup,
) allocationIndex {
	indexed := allocationIndex{
		racks:    make(map[allocationRackKey]rackAllocation),
		releases: make(map[allocate.Coordinate]allocate.Release),
	}
	for _, binding := range plan.Retained {
		if binding.Coordinate.Group.InventoryUID != inventoryUID {
			continue
		}
		key := allocationRackKey{group: binding.Coordinate.Group, rackIndex: binding.Coordinate.RackIndex}
		partition := indexed.racks[key]
		partition.retained = append(partition.retained, binding)
		indexed.racks[key] = partition
		indexed.indexed++
	}
	for _, binding := range plan.Assigned {
		if binding.Coordinate.Group.InventoryUID != inventoryUID {
			continue
		}
		key := allocationRackKey{group: binding.Coordinate.Group, rackIndex: binding.Coordinate.RackIndex}
		partition := indexed.racks[key]
		partition.assigned = append(partition.assigned, binding)
		indexed.racks[key] = partition
		indexed.indexed++
	}
	for _, release := range plan.Released {
		coordinate := release.Binding.Coordinate
		if coordinate.Group.InventoryUID != inventoryUID {
			continue
		}
		if _, ok := resolved[coordinate.Group.RackGroup]; !ok {
			continue
		}
		indexed.releases[coordinate] = release
		indexed.indexed++
	}
	return indexed
}

func allocationChangedRacks(plan allocate.Plan, group allocate.GroupKey, rackCount int32) []int32 {
	changed := make(map[int32]struct{}, len(plan.Assigned)+len(plan.Released))
	for _, binding := range plan.Assigned {
		if binding.Coordinate.Group == group && binding.Coordinate.RackIndex >= 0 &&
			binding.Coordinate.RackIndex < rackCount {
			changed[binding.Coordinate.RackIndex] = struct{}{}
		}
	}
	for _, release := range plan.Released {
		coordinate := release.Binding.Coordinate
		if coordinate.Group == group && coordinate.RackIndex >= 0 && coordinate.RackIndex < rackCount {
			changed[coordinate.RackIndex] = struct{}{}
		}
	}
	indices := make([]int32, 0, len(changed))
	for index := range changed {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	return indices
}

func indexGroupAllocation(
	plan allocate.Plan,
	group allocate.GroupKey,
	rackIndices []int32,
) allocationIndex {
	indexed := allocationIndex{
		racks:    make(map[allocationRackKey]rackAllocation, len(rackIndices)),
		releases: make(map[allocate.Coordinate]allocate.Release),
	}
	for _, rackIndex := range rackIndices {
		key := allocationRackKey{group: group, rackIndex: rackIndex}
		partition := rackAllocation{
			retained: allocationBindingsForRack(plan.Retained, key),
			assigned: allocationBindingsForRack(plan.Assigned, key),
		}
		indexed.racks[key] = partition
		indexed.indexed += int64(len(partition.retained) + len(partition.assigned))
		for _, release := range allocationReleasesForRack(plan.Released, key) {
			indexed.releases[release.Binding.Coordinate] = release
			indexed.indexed++
		}
	}
	return indexed
}

func allocationBindingsForRack(bindings []allocate.Binding, key allocationRackKey) []allocate.Binding {
	start, _ := slices.BinarySearchFunc(bindings, key, func(binding allocate.Binding, target allocationRackKey) int {
		return compareAllocationRackKey(
			allocationRackKey{group: binding.Coordinate.Group, rackIndex: binding.Coordinate.RackIndex},
			target,
		)
	})
	end := start
	for end < len(bindings) {
		current := allocationRackKey{
			group:     bindings[end].Coordinate.Group,
			rackIndex: bindings[end].Coordinate.RackIndex,
		}
		if compareAllocationRackKey(current, key) != 0 {
			break
		}
		end++
	}
	return bindings[start:end]
}

func allocationReleasesForRack(releases []allocate.Release, key allocationRackKey) []allocate.Release {
	start, _ := slices.BinarySearchFunc(releases, key, func(release allocate.Release, target allocationRackKey) int {
		coordinate := release.Binding.Coordinate
		return compareAllocationRackKey(
			allocationRackKey{group: coordinate.Group, rackIndex: coordinate.RackIndex},
			target,
		)
	})
	end := start
	for end < len(releases) {
		coordinate := releases[end].Binding.Coordinate
		current := allocationRackKey{group: coordinate.Group, rackIndex: coordinate.RackIndex}
		if compareAllocationRackKey(current, key) != 0 {
			break
		}
		end++
	}
	return releases[start:end]
}

func compareAllocationRackKey(a, b allocationRackKey) int {
	if order := compareGroupKeys(a.group, b.group); order != 0 {
		return order
	}
	return cmp.Compare(a.rackIndex, b.rackIndex)
}

func applyBindings(spec *mokkav1alpha1.SGPURackSpec, bindings []allocate.Binding) int64 {
	var applied int64
	for _, binding := range bindings {
		if binding.Coordinate.NodeIndex < 0 || int(binding.Coordinate.NodeIndex) >= len(spec.Nodes) {
			continue
		}
		spec.Nodes[binding.Coordinate.NodeIndex].NodeRef = &mokkav1alpha1.SGPUNodeReference{
			Name: binding.Node.Name, UID: binding.Node.UID,
		}
		applied++
	}
	return applied
}

func cleanupReason(reason allocate.ReleaseReason) CleanupReason {
	switch reason {
	case allocate.ReleaseCapacityShrink:
		return CleanupCapacityShrink
	case allocate.ReleaseNodeIneligible:
		return CleanupNodeIneligible
	case allocate.ReleaseSelectorMismatch:
		return CleanupSelectorMismatch
	case allocate.ReleaseGroupRemoved:
		return CleanupGroupRemoved
	default:
		return CleanupReason(reason)
	}
}

func ownershipConflict(rack *mokkav1alpha1.SGPURack, rackGroup string) OwnershipConflict {
	conflict := OwnershipConflict{RackName: rack.Name, RackGroup: rackGroup}
	if owner := metav1.GetControllerOf(rack); owner != nil {
		conflict.OwnerUID = owner.UID
	}
	return conflict
}

func rackSemanticEqual(a, b *mokkav1alpha1.SGPURack) bool {
	return equality.Semantic.DeepEqual(a.Spec, b.Spec) &&
		equality.Semantic.DeepEqual(a.Labels, b.Labels) &&
		equality.Semantic.DeepEqual(a.Annotations, b.Annotations) &&
		equality.Semantic.DeepEqual(a.Finalizers, b.Finalizers) &&
		equality.Semantic.DeepEqual(a.OwnerReferences, b.OwnerReferences)
}

func rackEmpty(rack *mokkav1alpha1.SGPURack) bool {
	for _, slot := range rack.Spec.Nodes {
		if slot.NodeRef != nil {
			return false
		}
	}
	return true
}

func removeString(values []string, remove string) []string {
	return slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == remove })
}

func appendOwnershipConflict(result *Result, err error) {
	var ownershipErr *OwnershipConflictError
	if errors.As(err, &ownershipErr) {
		result.OwnershipConflicts = append(result.OwnershipConflicts, ownershipErr.Conflict)
	}
}

func sortResult(result *Result) {
	slices.SortFunc(result.ProfileIssues, func(a, b ProfileIssue) int {
		if order := cmp.Compare(a.RackGroup, b.RackGroup); order != 0 {
			return order
		}
		return cmp.Compare(a.ProfileName, b.ProfileName)
	})
	slices.SortFunc(result.OwnershipConflicts, func(a, b OwnershipConflict) int { return cmp.Compare(a.RackName, b.RackName) })
	slices.SortFunc(result.CleanupNeeded, func(a, b CleanupNeeded) int {
		if order := cmp.Compare(a.RackName, b.RackName); order != 0 {
			return order
		}
		return cmp.Compare(a.Binding.Coordinate.NodeIndex, b.Binding.Coordinate.NodeIndex)
	})
}

func retryOnConflict(operation func() error) error {
	var lastErr error
	err := wait.ExponentialBackoff(wait.Backoff{
		Steps: 5, Duration: 10 * time.Millisecond, Factor: 1, Jitter: 0.1,
	}, func() (bool, error) {
		err := operation()
		if err == nil {
			return true, nil
		}
		var ownershipErr *OwnershipConflictError
		if errors.As(err, &ownershipErr) {
			return false, err
		}
		if !apierrors.IsConflict(err) {
			return false, err
		}
		lastErr = err
		return false, nil
	})
	if wait.Interrupted(err) {
		return lastErr
	}
	return err
}
