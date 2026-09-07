// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package status computes aggregate inventory and rack status from cached
// controller snapshots and writes only semantic changes.
package status

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
)

//nolint:revive // These reasons form one closed status-condition vocabulary.
const (
	// ReasonAccepted records a valid inventory declaration.
	ReasonAccepted              = "Accepted"
	ReasonInvalidInventory      = "InvalidInventory"
	ReasonCapacityExceeded      = controllerack.ReasonCapacityExceeded
	ReasonProfilesResolved      = "ProfilesResolved"
	ReasonProfileNotFound       = "ProfileNotFound"
	ReasonInvalidProfile        = "InvalidProfile"
	ReasonProgrammed            = "Programmed"
	ReasonReferencesUnresolved  = "ReferencesUnresolved"
	ReasonRackOwnershipConflict = "RackOwnershipConflict"
	ReasonRacksPending          = "RacksPending"
	ReasonRequestsSatisfied     = "RequestsSatisfied"
	ReasonPendingNodes          = "PendingNodes"
	ReasonPlacementConflicts    = "PlacementConflicts"
	ReasonProjectionIncomplete  = "ProjectionIncomplete"
	ReasonNodeMetadataConflict  = "NodeMetadataConflict"
	ReasonReady                 = "Ready"
	ReasonInvalidBindings       = "InvalidBindings"
	ReasonDuplicateBindings     = "DuplicateBindings"
)

// InventoryInput is one immutable informer and reconciliation snapshot.
type InventoryInput struct {
	Inventory  *mokkav1alpha1.SGPUInventory
	Profiles   map[string]*mokkav1alpha1.SGPURackProfile
	Racks      []*mokkav1alpha1.SGPURack
	Nodes      []*corev1.Node
	RackResult controllerack.Result
	Projection []controllerprojection.Outcome
}

// RackInput is the cached data needed to describe one rack.
type RackInput struct {
	Rack       *mokkav1alpha1.SGPURack
	Racks      []*mokkav1alpha1.SGPURack
	Nodes      []*corev1.Node
	Projection []controllerprojection.Outcome
}

// InventoryStatusWriter is the narrow live-client surface for inventory status.
type InventoryStatusWriter interface {
	Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPUInventory, error)
	UpdateStatus(context.Context, *mokkav1alpha1.SGPUInventory, metav1.UpdateOptions) (*mokkav1alpha1.SGPUInventory, error)
}

// RackStatusWriter is the narrow live-client surface for rack status.
type RackStatusWriter interface {
	Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error)
	UpdateStatus(context.Context, *mokkav1alpha1.SGPURack, metav1.UpdateOptions) (*mokkav1alpha1.SGPURack, error)
}

// Reconciler writes aggregate status only when its semantic value changes.
type Reconciler struct {
	inventories InventoryStatusWriter
	racks       RackStatusWriter
	now         func() metav1.Time

	pendingRackMu       sync.Mutex
	pendingRackStatuses map[string]pendingRackStatus
}

type pendingRackStatus struct {
	uid    types.UID
	status mokkav1alpha1.SGPURackStatus
}

// NewReconciler constructs an idempotent status writer.
func NewReconciler(inventories InventoryStatusWriter, racks RackStatusWriter, now func() metav1.Time) *Reconciler {
	if now == nil {
		now = metav1.Now
	}
	return &Reconciler{inventories: inventories, racks: racks, now: now}
}

// ReconcileInventory updates one exact inventory instance's aggregate status.
func (r *Reconciler) ReconcileInventory(ctx context.Context, input InventoryInput) (bool, error) {
	if input.Inventory == nil || input.Inventory.Name == "" || input.Inventory.UID == "" {
		return false, errors.New("inventory status requires exact name and UID")
	}
	changed := false
	err := retryOnConflict(func() error {
		latest, err := r.inventories.Get(ctx, input.Inventory.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if latest.UID != input.Inventory.UID {
			return apierrors.NewConflict(mokkav1alpha1.Resource("sgpuinventories"), input.Inventory.Name, errors.New("inventory UID changed"))
		}
		current := input
		current.Inventory = input.Inventory.DeepCopy()
		current.Inventory.Status = latest.Status
		desired := ComputeInventory(current, r.now())
		if equality.Semantic.DeepEqual(latest.Status, desired) {
			return nil
		}
		candidate := latest.DeepCopy()
		candidate.Status = desired
		if _, err := r.inventories.UpdateStatus(ctx, candidate, metav1.UpdateOptions{}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return changed, fmt.Errorf("update inventory %q status: %w", input.Inventory.Name, err)
	}
	return changed, nil
}

// ReconcileRack updates one exact rack instance's aggregate status.
//
//nolint:cyclop // Retry, deletion, recreation, and no-op paths are intentionally distinct.
func (r *Reconciler) ReconcileRack(ctx context.Context, input RackInput) (bool, error) {
	if input.Rack == nil || input.Rack.Name == "" || input.Rack.UID == "" {
		return false, errors.New("rack status requires exact name and UID")
	}
	now := r.now()
	desired := ComputeRack(input, now)
	if r.cachedRackStatusConverged(input.Rack, desired) {
		return false, nil
	}
	changed := false
	err := retryOnConflict(func() error {
		latest, err := r.racks.Get(ctx, input.Rack.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			r.clearPendingRackStatus(input.Rack.Name, input.Rack.UID)
			return nil
		}
		if err != nil {
			return err
		}
		if latest.UID != input.Rack.UID {
			r.clearPendingRackStatus(input.Rack.Name, input.Rack.UID)
			return nil
		}
		current := input
		current.Rack = input.Rack.DeepCopy()
		current.Rack.Status = latest.Status
		desired := ComputeRack(current, now)
		if equality.Semantic.DeepEqual(latest.Status, desired) {
			r.clearPendingRackStatus(input.Rack.Name, input.Rack.UID)
			return nil
		}
		candidate := latest.DeepCopy()
		candidate.Status = desired
		r.rememberPendingRackStatus(input.Rack.Name, input.Rack.UID, desired)
		if _, err := r.racks.UpdateStatus(ctx, candidate, metav1.UpdateOptions{}); apierrors.IsNotFound(err) {
			r.clearPendingRackStatus(input.Rack.Name, input.Rack.UID)
			return nil
		} else if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return changed, fmt.Errorf("update rack %q status: %w", input.Rack.Name, err)
	}
	return changed, nil
}

func (r *Reconciler) cachedRackStatusConverged(
	rack *mokkav1alpha1.SGPURack,
	desired mokkav1alpha1.SGPURackStatus,
) bool {
	r.pendingRackMu.Lock()
	defer r.pendingRackMu.Unlock()
	pending, exists := r.pendingRackStatuses[rack.Name]
	if exists && pending.uid != rack.UID {
		delete(r.pendingRackStatuses, rack.Name)
		exists = false
	}
	if exists && equality.Semantic.DeepEqual(rack.Status, pending.status) {
		delete(r.pendingRackStatuses, rack.Name)
		exists = false
	}
	return !exists && equality.Semantic.DeepEqual(rack.Status, desired)
}

func (r *Reconciler) rememberPendingRackStatus(
	name string,
	uid types.UID,
	status mokkav1alpha1.SGPURackStatus,
) {
	r.pendingRackMu.Lock()
	defer r.pendingRackMu.Unlock()
	if r.pendingRackStatuses == nil {
		r.pendingRackStatuses = make(map[string]pendingRackStatus)
	}
	// Until the informer observes this write, its older status may happen to
	// equal a newly desired value and is not safe for the no-op fast path.
	r.pendingRackStatuses[name] = pendingRackStatus{uid: uid, status: *status.DeepCopy()}
}

// ObserveRackStatus records that the informer has advanced past a pending
// status write, making its cached status safe for future no-op decisions.
func (r *Reconciler) ObserveRackStatus(rack *mokkav1alpha1.SGPURack) {
	if rack == nil {
		return
	}
	r.pendingRackMu.Lock()
	defer r.pendingRackMu.Unlock()
	pending, exists := r.pendingRackStatuses[rack.Name]
	if !exists {
		return
	}
	if pending.uid != rack.UID || equality.Semantic.DeepEqual(pending.status, rack.Status) {
		delete(r.pendingRackStatuses, rack.Name)
	}
}

// ForgetRackStatus releases pending status state for an informer-observed deletion.
func (r *Reconciler) ForgetRackStatus(name string, uid types.UID) {
	r.clearPendingRackStatus(name, uid)
}

func (r *Reconciler) clearPendingRackStatus(name string, uid types.UID) {
	r.pendingRackMu.Lock()
	defer r.pendingRackMu.Unlock()
	if pending, exists := r.pendingRackStatuses[name]; exists && pending.uid == uid {
		delete(r.pendingRackStatuses, name)
	}
}

type groupAggregate struct {
	status    mokkav1alpha1.RackGroupStatus
	capacity  controllerack.DeclaredCapacity
	selector  labels.Selector
	resolved  bool
	requested map[types.UID]struct{}
	projected int32
}

// ComputeInventory is pure apart from the caller-supplied transition time.
//
//nolint:cyclop // Aggregation keeps each bounded status and condition rule explicit.
func ComputeInventory(input InventoryInput, now metav1.Time) mokkav1alpha1.SGPUInventoryStatus {
	if input.Inventory == nil {
		return mokkav1alpha1.SGPUInventoryStatus{}
	}
	inventory := input.Inventory
	issues := make(map[string]controllerack.ProfileIssue, len(input.RackResult.ProfileIssues))
	for _, issue := range input.RackResult.ProfileIssues {
		issues[issue.RackGroup] = issue
	}

	groups := make(map[string]*groupAggregate, len(inventory.Spec.RackGroups))
	ordered := make([]string, 0, len(inventory.Spec.RackGroups))
	totalCapacity := controllerack.DeclaredCapacity{}
	capacityValid := true
	for _, declaration := range inventory.Spec.RackGroups {
		aggregate := &groupAggregate{
			status: mokkav1alpha1.RackGroupStatus{
				ID: declaration.ID, ProfileName: declaration.ProfileRef.Name,
			},
			selector: labels.Nothing(), requested: make(map[types.UID]struct{}),
		}
		if selector, err := groupSelector(declaration); err == nil {
			aggregate.selector = selector
		}
		profile := input.Profiles[declaration.ProfileRef.Name]
		_, hasIssue := issues[declaration.ID]
		if profile != nil && !hasIssue {
			aggregate.resolved = true
			capacity, err := controllerack.CapacityForGroup(declaration, profile)
			if err != nil {
				capacityValid = false
			} else {
				aggregate.capacity = capacity
				totalCapacity, err = controllerack.AddCapacity(totalCapacity, capacity)
				if err != nil {
					capacityValid = false
				}
			}
		}
		groups[declaration.ID] = aggregate
		ordered = append(ordered, declaration.ID)
	}
	slices.Sort(ordered)
	if capacityValid {
		if err := controllerack.ValidateSupportedCapacity(totalCapacity); err != nil {
			capacityValid = false
		}
	}
	if capacityValid {
		for _, aggregate := range groups {
			if !aggregate.resolved {
				continue
			}
			aggregate.status.Capacity, _ = controllerack.StatusCapacity(aggregate.capacity)
		}
	}

	requested := make(map[types.UID]struct{})
	liveNodes := make(map[types.UID]*corev1.Node, len(input.Nodes))
	for _, node := range input.Nodes {
		if node == nil || node.UID == "" {
			continue
		}
		liveNodes[node.UID] = node
		if node.Labels[allocate.EligibleNodeLabel] != "true" {
			continue
		}
		for _, aggregate := range groups {
			if aggregate.selector.Matches(labels.Set(node.Labels)) {
				aggregate.requested[node.UID] = struct{}{}
				requested[node.UID] = struct{}{}
			}
		}
	}

	for _, rack := range input.Racks {
		if !ownedByInventory(rack, inventory) {
			continue
		}
		aggregate := groups[rack.Spec.Identity.RackGroup]
		if aggregate == nil {
			continue
		}
		for i := range rack.Spec.Nodes {
			slot := &rack.Spec.Nodes[i]
			if slot.NodeRef == nil {
				continue
			}
			node := liveNodes[slot.NodeRef.UID]
			if node == nil || node.Name != slot.NodeRef.Name {
				continue
			}
			aggregate.status.Usage.AllocatedNodes++
			if controllerprojection.MatchesBinding(node, rack, slot) {
				aggregate.projected++
			}
		}
	}

	for _, pending := range input.RackResult.Allocation.Pending {
		matches := matchingGroups(pending, groups)
		if len(matches) == 1 {
			groups[matches[0]].status.Usage.PendingNodes++
		}
	}
	conflicting := make(map[types.UID]struct{})
	for _, conflict := range input.RackResult.Allocation.Conflicts {
		affected := conflictGroups(conflict, inventory.UID)
		if len(affected) == 0 {
			continue
		}
		conflicting[conflict.Node.UID] = struct{}{}
	}

	result := mokkav1alpha1.SGPUInventoryStatus{RackGroupsSummary: strings.Join(ordered, ",")}
	if capacityValid {
		result.Capacity, _ = controllerack.StatusCapacity(totalCapacity)
	}
	var projectedNodes int32
	for _, id := range ordered {
		aggregate := groups[id]
		aggregate.status.Usage.RequestedNodes = int32(len(aggregate.requested))
		aggregate.status.Usage.AvailableNodes = max(0, aggregate.status.Capacity.Nodes-aggregate.status.Usage.AllocatedNodes)
		result.Usage.AllocatedNodes += aggregate.status.Usage.AllocatedNodes
		result.Usage.AvailableNodes += aggregate.status.Usage.AvailableNodes
		result.Usage.PendingNodes += aggregate.status.Usage.PendingNodes
		projectedNodes += aggregate.projected
		result.RackGroups = append(result.RackGroups, aggregate.status)
	}
	result.Usage.RequestedNodes = int32(len(requested))
	result.Conditions = mergeConditions(
		inventory.Status.Conditions,
		inventoryConditions(input, result, groups, projectedNodes, int32(len(conflicting))),
		inventory.Generation,
		now,
	)
	return result
}

// ComputeRack counts spec assignments and only exact successful projections.
//
//nolint:cyclop // Rack readiness checks every exact binding and projection failure mode.
func ComputeRack(input RackInput, now metav1.Time) mokkav1alpha1.SGPURackStatus {
	if input.Rack == nil {
		return mokkav1alpha1.SGPURackStatus{}
	}
	rack := input.Rack
	result := mokkav1alpha1.SGPURackStatus{ObservedGeneration: rack.Generation}
	liveNodes := make(map[types.UID]*corev1.Node, len(input.Nodes))
	for _, node := range input.Nodes {
		if node != nil && node.UID != "" {
			liveNodes[node.UID] = node
		}
	}
	invalid := rack.Spec.InventoryRef.Name == "" || rack.Spec.InventoryRef.UID == "" ||
		rack.Spec.ProfileRef.Name == "" || rack.Spec.ProfileRef.UID == ""
	duplicateUIDs := duplicateUIDs(input.Racks)
	projectionByBinding := indexProjection(input.Projection)
	var projectedSlots int32
	hasDuplicate := false
	seenSlots := make(map[int32]struct{}, len(rack.Spec.Nodes))
	for i := range rack.Spec.Nodes {
		slot := &rack.Spec.Nodes[i]
		if _, exists := seenSlots[slot.Index]; exists {
			invalid = true
		}
		seenSlots[slot.Index] = struct{}{}
		if slot.NodeRef == nil {
			continue
		}
		result.AssignedNodes++
		if slot.NodeRef.Name == "" || slot.NodeRef.UID == "" {
			invalid = true
			continue
		}
		if _, duplicated := duplicateUIDs[slot.NodeRef.UID]; duplicated {
			hasDuplicate = true
		}
		node := liveNodes[slot.NodeRef.UID]
		if node == nil || node.Name != slot.NodeRef.Name {
			invalid = true
			continue
		}
		if controllerprojection.MatchesBinding(node, rack, slot) {
			projectedSlots++
		}
	}

	ready := metav1.Condition{
		Type: mokkav1alpha1.RackConditionReady, Status: metav1.ConditionTrue,
		Reason: ReasonReady, Message: "Rack bindings are valid.",
	}
	switch {
	case hasDuplicate:
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, ReasonDuplicateBindings, "A Node UID is bound to more than one logical rack Node."
	case invalid:
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, ReasonInvalidBindings, "The rack contains an invalid or non-live binding."
	case projectedSlots != result.AssignedNodes:
		ready.Status, ready.Reason = metav1.ConditionFalse, ReasonProjectionIncomplete
		ready.Message = fmt.Sprintf("%d of %d assigned Nodes are projected.", projectedSlots, result.AssignedNodes)
		if rackHasMetadataConflict(projectionByBinding, rack) {
			ready.Reason = ReasonNodeMetadataConflict
		}
	}
	result.Conditions = mergeConditions(
		rack.Status.Conditions,
		[]metav1.Condition{ready},
		rack.Generation,
		now,
	)
	return result
}

//nolint:cyclop // The API's four conditions intentionally summarize independent controller phases.
func inventoryConditions(
	input InventoryInput,
	status mokkav1alpha1.SGPUInventoryStatus,
	groups map[string]*groupAggregate,
	projectedNodes int32,
	conflictingNodes int32,
) []metav1.Condition {
	accepted := metav1.Condition{
		Type: mokkav1alpha1.InventoryConditionAccepted, Status: metav1.ConditionTrue,
		Reason: ReasonAccepted, Message: "The inventory configuration is valid.",
	}
	if !input.RackResult.Accepted {
		reason := input.RackResult.ValidationReason
		if reason == "" {
			reason = ReasonInvalidInventory
		}
		accepted.Status, accepted.Reason, accepted.Message = metav1.ConditionFalse, reason, input.RackResult.ValidationError
	}

	resolved := metav1.Condition{
		Type: mokkav1alpha1.InventoryConditionResolvedRefs, Status: metav1.ConditionTrue,
		Reason: ReasonProfilesResolved, Message: "All referenced profiles are resolved.",
	}
	if !input.RackResult.ResolvedRefs || hasUnresolvedGroup(groups) {
		resolved.Status, resolved.Reason, resolved.Message = metav1.ConditionFalse, ReasonProfileNotFound, "One or more referenced profiles are missing."
		for _, issue := range input.RackResult.ProfileIssues {
			if issue.Reason != "NotFound" {
				resolved.Reason, resolved.Message = ReasonInvalidProfile, "One or more referenced profiles are invalid."
				break
			}
		}
	}

	programmed := metav1.Condition{
		Type: mokkav1alpha1.InventoryConditionProgrammed, Status: metav1.ConditionTrue,
		Reason: ReasonProgrammed, Message: "All desired racks and Node projections are programmed.",
	}
	switch {
	case accepted.Status == metav1.ConditionFalse:
		programmed.Status, programmed.Reason, programmed.Message = metav1.ConditionFalse, accepted.Reason, "Programming is blocked by an invalid inventory."
	case resolved.Status == metav1.ConditionFalse:
		programmed.Status, programmed.Reason, programmed.Message = metav1.ConditionFalse, ReasonReferencesUnresolved, "Programming is blocked by unresolved profiles."
	case len(input.RackResult.OwnershipConflicts) > 0:
		programmed.Status, programmed.Reason, programmed.Message = metav1.ConditionFalse, ReasonRackOwnershipConflict, "A deterministic rack name is owned by another object."
	case !desiredRacksPresent(input):
		programmed.Status, programmed.Reason, programmed.Message = metav1.ConditionFalse, ReasonRacksPending, "One or more desired racks are not present in the cache."
	case projectedNodes != status.Usage.AllocatedNodes:
		programmed.Status, programmed.Reason = metav1.ConditionFalse, ReasonProjectionIncomplete
		programmed.Message = fmt.Sprintf("%d of %d allocated Nodes are projected.", projectedNodes, status.Usage.AllocatedNodes)
		if inventoryHasMetadataConflict(input) {
			programmed.Reason = ReasonNodeMetadataConflict
		}
	}

	requests := metav1.Condition{
		Type: mokkav1alpha1.InventoryConditionRequestsSatisfied, Status: metav1.ConditionTrue,
		Reason: ReasonRequestsSatisfied, Message: "All unambiguous requests fit available capacity.",
	}
	if conflictingNodes > 0 {
		requests.Status = metav1.ConditionFalse
		requests.Reason = ReasonPlacementConflicts
		requests.Message = fmt.Sprintf("%d requested Nodes have placement conflicts.", conflictingNodes)
	} else if status.Usage.PendingNodes > 0 {
		requests.Status = metav1.ConditionFalse
		requests.Reason = ReasonPendingNodes
		requests.Message = fmt.Sprintf("%d requested Nodes are pending capacity.", status.Usage.PendingNodes)
	}

	return []metav1.Condition{accepted, resolved, programmed, requests}
}

func mergeConditions(old, desired []metav1.Condition, generation int64, now metav1.Time) []metav1.Condition {
	oldByType := make(map[string]metav1.Condition, len(old))
	for _, condition := range old {
		oldByType[condition.Type] = condition
	}
	merged := make([]metav1.Condition, 0, len(desired))
	for _, condition := range desired {
		condition.ObservedGeneration = generation
		condition.LastTransitionTime = now
		if previous, exists := oldByType[condition.Type]; exists &&
			previous.Status == condition.Status && previous.Reason == condition.Reason {
			condition.LastTransitionTime = previous.LastTransitionTime
		}
		merged = append(merged, condition)
	}
	slices.SortFunc(merged, func(a, b metav1.Condition) int {
		return cmp.Compare(a.Type, b.Type)
	})
	return merged
}

func groupSelector(group mokkav1alpha1.RackGroup) (labels.Selector, error) {
	if group.Placement == nil || group.Placement.NodeSelector == nil {
		return labels.Everything(), nil
	}
	return allocate.CompilePlacementSelector(group.Placement.NodeSelector)
}

func matchingGroups(node allocate.Node, groups map[string]*groupAggregate) []string {
	matches := make([]string, 0, 1)
	if node.Labels[allocate.EligibleNodeLabel] != "true" {
		return matches
	}
	for id, aggregate := range groups {
		if aggregate.resolved && aggregate.selector.Matches(labels.Set(node.Labels)) {
			matches = append(matches, id)
		}
	}
	slices.Sort(matches)
	return matches
}

func conflictGroups(conflict allocate.Conflict, inventoryUID types.UID) map[string]struct{} {
	groups := make(map[string]struct{})
	for _, candidate := range conflict.Candidates {
		if candidate.InventoryUID == inventoryUID {
			groups[candidate.RackGroup] = struct{}{}
		}
	}
	for _, binding := range conflict.Bindings {
		if binding.Coordinate.Group.InventoryUID == inventoryUID {
			groups[binding.Coordinate.Group.RackGroup] = struct{}{}
		}
	}
	return groups
}

type projectionKey struct {
	rackName  string
	rackUID   types.UID
	nodeIndex int32
	nodeName  string
	nodeUID   types.UID
}

func indexProjection(outcomes []controllerprojection.Outcome) map[projectionKey]controllerprojection.Outcome {
	indexed := make(map[projectionKey]controllerprojection.Outcome, len(outcomes))
	for _, outcome := range outcomes {
		indexed[projectionKeyForOutcome(outcome)] = outcome
	}
	return indexed
}

func duplicateUIDs(racks []*mokkav1alpha1.SGPURack) map[types.UID]struct{} {
	counts := make(map[types.UID]int)
	for _, rack := range racks {
		if rack == nil {
			continue
		}
		for _, slot := range rack.Spec.Nodes {
			if slot.NodeRef != nil && slot.NodeRef.UID != "" {
				counts[slot.NodeRef.UID]++
			}
		}
	}
	duplicates := make(map[types.UID]struct{})
	for uid, count := range counts {
		if count > 1 {
			duplicates[uid] = struct{}{}
		}
	}
	return duplicates
}

func ownedByInventory(rack *mokkav1alpha1.SGPURack, inventory *mokkav1alpha1.SGPUInventory) bool {
	if rack == nil ||
		rack.Spec.InventoryRef.Name != inventory.Name ||
		rack.Spec.InventoryRef.UID != inventory.UID {
		return false
	}
	owner := metav1.GetControllerOf(rack)
	return owner != nil &&
		owner.APIVersion == mokkav1alpha1.SchemeGroupVersion.String() &&
		owner.Kind == "SGPUInventory" &&
		owner.Name == inventory.Name && owner.UID == inventory.UID
}

//nolint:cyclop // Presence requires exact owner, group, index, and rendered spec identity.
func desiredRacksPresent(input InventoryInput) bool {
	present := make(map[string]map[int32]struct{})
	for _, rack := range input.Racks {
		if !ownedByInventory(rack, input.Inventory) || rack.DeletionTimestamp != nil {
			continue
		}
		if present[rack.Spec.Identity.RackGroup] == nil {
			present[rack.Spec.Identity.RackGroup] = make(map[int32]struct{})
		}
		present[rack.Spec.Identity.RackGroup][rack.Spec.Identity.RackIndex] = struct{}{}
	}
	issues := make(map[string]struct{}, len(input.RackResult.ProfileIssues))
	for _, issue := range input.RackResult.ProfileIssues {
		issues[issue.RackGroup] = struct{}{}
	}
	for _, group := range input.Inventory.Spec.RackGroups {
		if _, unresolved := issues[group.ID]; unresolved || input.Profiles[group.ProfileRef.Name] == nil {
			continue
		}
		for index := int32(0); index < group.Count; index++ {
			if _, exists := present[group.ID][index]; !exists {
				return false
			}
		}
	}
	return true
}

func hasUnresolvedGroup(groups map[string]*groupAggregate) bool {
	for _, group := range groups {
		if !group.resolved {
			return true
		}
	}
	return false
}

func inventoryHasMetadataConflict(input InventoryInput) bool {
	owned := make(map[string]struct{})
	for _, rack := range input.Racks {
		if ownedByInventory(rack, input.Inventory) {
			owned[rack.Name] = struct{}{}
		}
	}
	for _, outcome := range input.Projection {
		if _, ok := owned[outcome.RackName]; ok &&
			outcome.State == controllerprojection.StateConflict &&
			outcome.Reason == controllerprojection.ReasonNodeMetadataConflict {
			return true
		}
	}
	return false
}

func rackHasMetadataConflict(outcomes map[projectionKey]controllerprojection.Outcome, rack *mokkav1alpha1.SGPURack) bool {
	for i := range rack.Spec.Nodes {
		slot := &rack.Spec.Nodes[i]
		if slot.NodeRef == nil {
			continue
		}
		outcome, exists := outcomes[projectionKeyForBinding(rack, slot)]
		if exists && outcome.State == controllerprojection.StateConflict && outcome.Reason == controllerprojection.ReasonNodeMetadataConflict {
			return true
		}
	}
	return false
}

func projectionKeyForOutcome(outcome controllerprojection.Outcome) projectionKey {
	return projectionKey{
		rackName: outcome.RackName, rackUID: outcome.RackUID, nodeIndex: outcome.NodeIndex,
		nodeName: outcome.NodeName, nodeUID: outcome.NodeUID,
	}
}

func projectionKeyForBinding(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackNode) projectionKey {
	return projectionKey{
		rackName: rack.Name, rackUID: rack.UID, nodeIndex: slot.Index,
		nodeName: slot.NodeRef.Name, nodeUID: slot.NodeRef.UID,
	}
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
