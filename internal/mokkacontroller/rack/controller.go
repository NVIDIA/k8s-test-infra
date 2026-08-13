// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

const (
	InventoryFinalizer = "mokka.nvidia.com/inventory-cleanup"
	RackFinalizer      = "mokka.nvidia.com/rack-cleanup"

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

// RackMutations is the narrow live-client surface used for rack writes.
type RackMutations interface {
	Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error)
	Create(context.Context, *mokkav1alpha1.SGPURack, metav1.CreateOptions) (*mokkav1alpha1.SGPURack, error)
	Update(context.Context, *mokkav1alpha1.SGPURack, metav1.UpdateOptions) (*mokkav1alpha1.SGPURack, error)
	Delete(context.Context, string, metav1.DeleteOptions) error
}

// CleanupReason identifies why a durable binding must be removed.
type CleanupReason string

const (
	CleanupCapacityShrink    CleanupReason = "CapacityShrink"
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
	Binding  allocate.Binding
	Reason   CleanupReason
}

// CleanupGate is the acknowledgement seam implemented by Node projection.
// It must return true only when metadata for this exact binding is absent.
type CleanupGate interface {
	Ready(CleanupNeeded) bool
}

// CleanupGateFunc adapts a function to CleanupGate.
type CleanupGateFunc func(CleanupNeeded) bool

func (f CleanupGateFunc) Ready(cleanup CleanupNeeded) bool { return f(cleanup) }

// ProfileIssue identifies an unresolved or invalid group profile.
type ProfileIssue struct {
	RackGroup   string
	ProfileName string
	Reason      string
}

// OwnershipConflict identifies a deterministic rack name that cannot be adopted.
type OwnershipConflict struct {
	RackName  string
	RackGroup string
	OwnerUID  types.UID
}

// Result contains status inputs and explicit cross-controller cleanup work.
// Expected configuration problems are data, not retryable errors.
type Result struct {
	Accepted           bool
	ResolvedRefs       bool
	ValidationError    string
	ProfileIssues      []ProfileIssue
	OwnershipConflicts []OwnershipConflict
	CleanupNeeded      []CleanupNeeded
	Allocation         allocate.Plan
	Work               WorkStats
	Changed            bool
}

// WorkStats exposes deterministic operation counts for reconciliation scale
// regressions without depending on wall-clock timing.
type WorkStats struct {
	AllocationsIndexed int64
	BindingsApplied    int64
	RacksReconciled    int64
}

// Reconciler materializes one cached inventory key at a time.
type Reconciler struct {
	cache       Cache
	inventories InventoryMutations
	racks       RackMutations
	cleanup     CleanupGate
}

// NewReconciler constructs a keyed rack reconciler.
func NewReconciler(
	cache Cache,
	inventories InventoryMutations,
	racks RackMutations,
	cleanup CleanupGate,
) *Reconciler {
	return &Reconciler{cache: cache, inventories: inventories, racks: racks, cleanup: cleanup}
}

func (r *Reconciler) Reconcile(ctx context.Context, key string) (Result, error) {
	return r.reconcile(ctx, key, nil)
}

// ReconcileGroup applies one exact allocation group while retaining a global
// allocation snapshot for cross-inventory overlap and duplicate detection.
func (r *Reconciler) ReconcileGroup(ctx context.Context, key allocate.GroupKey) (Result, error) {
	return r.reconcile(ctx, key.InventoryName, &key)
}

func (r *Reconciler) reconcile(ctx context.Context, key string, requestedGroup *allocate.GroupKey) (Result, error) {
	result := Result{Accepted: true, ResolvedRefs: true}
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

	var ownedRacks []*mokkav1alpha1.SGPURack
	if requestedGroup != nil && inventory.DeletionTimestamp == nil {
		ownedRacks, err = r.cache.RacksByInventoryGroup(inventory.UID, requestedGroup.RackGroup)
	} else {
		ownedRacks, err = r.cache.RacksByInventoryUID(inventory.UID)
	}
	if err != nil {
		return result, fmt.Errorf("get racks for inventory %q from cache: %w", key, err)
	}
	ownedRacks = filterOwnedRacks(ownedRacks, inventory)

	if inventory.DeletionTimestamp != nil {
		return r.reconcileInventoryDeletion(ctx, inventory, ownedRacks, result)
	}
	if !slices.Contains(inventory.Finalizers, InventoryFinalizer) {
		if requestedGroup != nil {
			return result, nil
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

	if err := validateInventory(inventory); err != nil {
		result.Accepted = false
		result.ValidationError = err.Error()
		return result, nil
	}

	resolved, issues, err := r.resolveGroups(inventory)
	if err != nil {
		return result, err
	}
	result.ProfileIssues = issues
	result.ResolvedRefs = len(issues) == 0
	workGroups := resolved
	if requestedGroup != nil {
		workGroups = slices.DeleteFunc(slices.Clone(resolved), func(group resolvedGroup) bool {
			return group.key != *requestedGroup
		})
	}

	allocation, err := r.allocationPlan()
	if err != nil {
		return result, err
	}
	result.Allocation = allocation

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
			return result, err
		}
		result.Changed = result.Changed || changed
		result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
	}

	desiredNames := make(map[string]struct{})
	for _, group := range workGroups {
		for rackIndex := int32(0); rackIndex < group.group.Count; rackIndex++ {
			desiredNames[materialize.RackName(inventory.Name, inventory.UID, group.group.ID, rackIndex)] = struct{}{}
		}
	}

	if requestedGroup == nil {
		for _, existing := range ownedRacks {
			if existing.DeletionTimestamp != nil {
				continue
			}
			if _, keepLastGood := unresolved[existing.Spec.Identity.RackGroup]; keepLastGood {
				continue
			}
			if _, desired := desiredNames[existing.Name]; desired {
				continue
			}
			reason := CleanupGroupRemoved
			if group, exists := resolvedByID[existing.Spec.Identity.RackGroup]; exists &&
				existing.Spec.Identity.RackIndex >= group.group.Count {
				reason = CleanupCapacityShrink
			}
			changed, cleanup, err := r.retireRack(ctx, inventory, existing, reason)
			if err != nil {
				return result, err
			}
			result.Changed = result.Changed || changed
			result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
		}
	}

	allocations := indexAllocation(allocation, inventory.UID, resolvedByID)
	result.Work.AllocationsIndexed = allocations.indexed
	for _, group := range workGroups {
		for rackIndex := int32(0); rackIndex < group.group.Count; rackIndex++ {
			result.Work.RacksReconciled++
			rendered, err := materialize.RenderRack(materialize.RackInput{
				InventoryName: inventory.Name,
				InventoryUID:  inventory.UID,
				Group:         group.group,
				RackIndex:     rackIndex,
				Profile:       group.profile,
			})
			if err != nil {
				return result, fmt.Errorf("render rack group %q index %d: %w", group.group.ID, rackIndex, err)
			}
			if _, blocked := blockedNames[rendered.Name]; blocked {
				continue
			}
			existing := existingByName[rendered.Name]
			if existing == nil {
				cached, err := r.cache.Rack(rendered.Name)
				switch {
				case err == nil:
					existing = cached
				case apierrors.IsNotFound(err):
					if requestedGroup != nil {
						continue
					}
				default:
					return result, fmt.Errorf("get rack %q from cache: %w", rendered.Name, err)
				}
			}
			if existing != nil && !controlledByInventory(existing, inventory) {
				result.OwnershipConflicts = append(result.OwnershipConflicts, ownershipConflict(existing, group.group.ID))
				continue
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

			changed, conflict, err := r.createOrUpdateRack(ctx, inventory, existing, rendered.Name, targetSpec)
			if err != nil {
				return result, err
			}
			result.Changed = result.Changed || changed
			if conflict != nil {
				result.OwnershipConflicts = append(result.OwnershipConflicts, *conflict)
			}
		}
	}

	sortResult(&result)
	return result, nil
}

type resolvedGroup struct {
	group   mokkav1alpha1.SGPURackGroup
	profile *mokkav1alpha1.SGPUProfile
	key     allocate.GroupKey
}

func (r *Reconciler) resolveGroups(inventory *mokkav1alpha1.SGPUInventory) ([]resolvedGroup, []ProfileIssue, error) {
	resolved := make([]resolvedGroup, 0, len(inventory.Spec.RackGroups))
	issues := make([]ProfileIssue, 0)
	for _, group := range inventory.Spec.RackGroups {
		profile, err := r.cache.Profile(group.ProfileRef.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				issues = append(issues, ProfileIssue{RackGroup: group.ID, ProfileName: group.ProfileRef.Name, Reason: "NotFound"})
				continue
			}
			return nil, nil, fmt.Errorf("get profile %q from cache: %w", group.ProfileRef.Name, err)
		}
		if err := materialize.ValidateProfile(profile.Spec); err != nil {
			issues = append(issues, ProfileIssue{RackGroup: group.ID, ProfileName: group.ProfileRef.Name, Reason: err.Error()})
			continue
		}
		if group.Count > 0 {
			_, err := materialize.RenderRack(materialize.RackInput{
				InventoryName: inventory.Name, InventoryUID: inventory.UID,
				Group: group, RackIndex: 0, Profile: profile,
			})
			if err != nil {
				issues = append(issues, ProfileIssue{RackGroup: group.ID, ProfileName: group.ProfileRef.Name, Reason: err.Error()})
				continue
			}
		}
		resolved = append(resolved, resolvedGroup{
			group:   group,
			profile: profile,
			key:     allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: group.ID},
		})
	}
	return resolved, issues, nil
}

func (r *Reconciler) allocationPlan() (allocate.Plan, error) {
	inventories, err := r.cache.Inventories()
	if err != nil {
		return allocate.Plan{}, fmt.Errorf("list inventories from cache: %w", err)
	}
	groups := make([]allocate.Group, 0)
	inventoriesByUID := make(map[types.UID]*mokkav1alpha1.SGPUInventory, len(inventories))
	for _, inventory := range inventories {
		inventoriesByUID[inventory.UID] = inventory
		if inventory.DeletionTimestamp != nil || validateInventory(inventory) != nil {
			continue
		}
		resolved, _, err := r.resolveGroups(inventory)
		if err != nil {
			return allocate.Plan{}, err
		}
		for _, group := range resolved {
			var selector *metav1.LabelSelector
			if group.group.Placement != nil {
				selector = group.group.Placement.NodeSelector
			}
			groups = append(groups, allocate.Group{
				Key: group.key, Selector: selector,
				Racks: group.group.Count, SlotsPerRack: group.profile.Spec.Rack.NodesPerRack,
			})
		}
	}

	racks, err := r.cache.Racks()
	if err != nil {
		return allocate.Plan{}, fmt.Errorf("list racks from cache: %w", err)
	}
	bindings := make([]allocate.Binding, 0)
	for _, rack := range racks {
		inventory := inventoriesByUID[rack.Spec.InventoryRef.UID]
		if inventory == nil || !controlledByInventory(rack, inventory) {
			continue
		}
		for _, slot := range rack.Spec.Slots {
			if slot.NodeRef == nil {
				continue
			}
			bindings = append(bindings, allocate.Binding{
				Coordinate: allocate.Coordinate{
					Group: allocate.GroupKey{
						InventoryName: inventory.Name, InventoryUID: inventory.UID,
						RackGroup: rack.Spec.Identity.RackGroup,
					},
					RackIndex: rack.Spec.Identity.RackIndex, SlotIndex: slot.Index,
				},
				Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
			})
		}
	}

	nodes, err := r.cache.Nodes()
	if err != nil {
		return allocate.Plan{}, fmt.Errorf("list Nodes from cache: %w", err)
	}
	allocationNodes := make([]allocate.Node, 0, len(nodes))
	for _, node := range nodes {
		allocationNodes = append(allocationNodes, allocate.Node{
			Name: node.Name, UID: node.UID,
			CreationTimestamp: node.CreationTimestamp.Time, Labels: node.Labels,
		})
	}
	plan, err := allocate.Allocate(allocate.Input{Groups: groups, Nodes: allocationNodes, Bindings: bindings})
	if err != nil {
		return allocate.Plan{}, fmt.Errorf("allocate rack bindings: %w", err)
	}
	return plan, nil
}

func (r *Reconciler) preservePendingReleases(
	existing *mokkav1alpha1.SGPURack,
	target *mokkav1alpha1.SGPURackSpec,
	releases map[allocate.Coordinate]allocate.Release,
) []CleanupNeeded {
	cleanup := make([]CleanupNeeded, 0)
	targetSlots := make(map[int32]*mokkav1alpha1.SGPURackSlot, len(target.Slots))
	for i := range target.Slots {
		targetSlots[target.Slots[i].Index] = &target.Slots[i]
	}
	for _, slot := range existing.Spec.Slots {
		if slot.NodeRef == nil {
			continue
		}
		coordinate := allocate.Coordinate{
			Group: allocate.GroupKey{
				InventoryName: existing.Spec.InventoryRef.Name,
				InventoryUID:  existing.Spec.InventoryRef.UID,
				RackGroup:     existing.Spec.Identity.RackGroup,
			},
			RackIndex: existing.Spec.Identity.RackIndex, SlotIndex: slot.Index,
		}
		release, found := releases[coordinate]
		if !found {
			continue
		}
		needed := CleanupNeeded{RackName: existing.Name, Binding: release.Binding, Reason: cleanupReason(release.Reason)}
		if r.cleanup != nil && r.cleanup.Ready(needed) {
			continue
		}
		cleanup = append(cleanup, needed)
		if targetSlot := targetSlots[slot.Index]; targetSlot != nil {
			targetSlot.NodeRef = slot.NodeRef.DeepCopy()
			continue
		}
		target.Slots = append(target.Slots, *slot.DeepCopy())
	}
	slices.SortFunc(target.Slots, func(a, b mokkav1alpha1.SGPURackSlot) int { return cmp.Compare(a.Index, b.Index) })
	return cleanup
}

func (r *Reconciler) retireRack(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	rack *mokkav1alpha1.SGPURack,
	reason CleanupReason,
) (bool, []CleanupNeeded, error) {
	clearSlots := make(map[int32]types.UID)
	cleanup := make([]CleanupNeeded, 0)
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef == nil {
			continue
		}
		binding := allocate.Binding{
			Coordinate: allocate.Coordinate{
				Group:     allocate.GroupKey{InventoryName: inventory.Name, InventoryUID: inventory.UID, RackGroup: rack.Spec.Identity.RackGroup},
				RackIndex: rack.Spec.Identity.RackIndex, SlotIndex: slot.Index,
			},
			Node: allocate.NodeReference{Name: slot.NodeRef.Name, UID: slot.NodeRef.UID},
		}
		needed := CleanupNeeded{RackName: rack.Name, Binding: binding, Reason: reason}
		if r.cleanup != nil && r.cleanup.Ready(needed) {
			clearSlots[slot.Index] = slot.NodeRef.UID
			continue
		}
		cleanup = append(cleanup, needed)
	}

	changed, empty, latest, err := r.mutateRack(ctx, inventory, rack, func(latest *mokkav1alpha1.SGPURack) bool {
		mutated := false
		for i := range latest.Spec.Slots {
			ref := latest.Spec.Slots[i].NodeRef
			if ref == nil {
				continue
			}
			if uid, clear := clearSlots[latest.Spec.Slots[i].Index]; clear && uid == ref.UID {
				latest.Spec.Slots[i].NodeRef = nil
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
			return result, err
		}
		result.Changed = result.Changed || changed
		result.CleanupNeeded = append(result.CleanupNeeded, cleanup...)
		if len(cleanup) > 0 {
			allGone = false
		}
	}
	if allGone && slices.Contains(inventory.Finalizers, InventoryFinalizer) {
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
	}
	sortResult(&result)
	return result, nil
}

func (r *Reconciler) createOrUpdateRack(
	ctx context.Context,
	inventory *mokkav1alpha1.SGPUInventory,
	existing *mokkav1alpha1.SGPURack,
	name string,
	spec mokkav1alpha1.SGPURackSpec,
) (bool, *OwnershipConflict, error) {
	if existing == nil {
		desired := newRack(inventory, name, spec)
		_, err := r.racks.Create(ctx, desired, metav1.CreateOptions{})
		if err == nil {
			return true, nil, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return false, nil, fmt.Errorf("create rack %q: %w", name, err)
		}
		latest, getErr := r.racks.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return false, nil, fmt.Errorf("get concurrently created rack %q: %w", name, getErr)
		}
		if !controlledByInventory(latest, inventory) {
			conflict := ownershipConflict(latest, spec.Identity.RackGroup)
			return false, &conflict, nil
		}
		return false, nil, apierrors.NewConflict(mokkav1alpha1.Resource("sgpuracks"), name, errors.New("informer snapshot is stale after concurrent rack creation"))
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
	return changed, nil, err
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
			return apierrors.NewConflict(mokkav1alpha1.Resource("sgpuracks"), base.Name, errors.New("rack ownership changed"))
		}
		candidate := latest.DeepCopy()
		if !mutate(candidate) {
			return nil
		}
		updated, err := r.racks.Update(ctx, candidate, metav1.UpdateOptions{})
		if err != nil {
			return err
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

func validateInventory(inventory *mokkav1alpha1.SGPUInventory) error {
	if inventory.Name == "" || inventory.UID == "" {
		return fmt.Errorf("inventory identity must include name and UID")
	}
	if len(inventory.Spec.RackGroups) > 64 {
		return fmt.Errorf("rackGroups must contain at most 64 entries")
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
		if group.Count < 0 || group.Count > 100_000 {
			return fmt.Errorf("rack group %q count is outside [0,100000]", group.ID)
		}
		if group.ProfileRef.Name == "" {
			return fmt.Errorf("rack group %q profileRef.name must not be empty", group.ID)
		}
		if group.Placement != nil && group.Placement.NodeSelector != nil {
			if _, err := metav1.LabelSelectorAsSelector(group.Placement.NodeSelector); err != nil {
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

func applyBindings(spec *mokkav1alpha1.SGPURackSpec, bindings []allocate.Binding) int64 {
	var applied int64
	for _, binding := range bindings {
		if binding.Coordinate.SlotIndex < 0 || int(binding.Coordinate.SlotIndex) >= len(spec.Slots) {
			continue
		}
		spec.Slots[binding.Coordinate.SlotIndex].NodeRef = &mokkav1alpha1.SGPUNodeReference{
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
	for _, slot := range rack.Spec.Slots {
		if slot.NodeRef != nil {
			return false
		}
	}
	return true
}

func removeString(values []string, remove string) []string {
	return slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == remove })
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
		return cmp.Compare(a.Binding.Coordinate.SlotIndex, b.Binding.Coordinate.SlotIndex)
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
