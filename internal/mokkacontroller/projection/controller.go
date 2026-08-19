// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

// Package projection derives the small controller-owned Node metadata surface
// from durable SGPURack bindings.
package projection

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/metadata"
)

//nolint:revive // These keys are one versioned projection metadata protocol.
const (
	// FieldManager owns only the compact metadata derived from rack bindings.
	FieldManager    = "mokka-controller"
	operationShards = 256

	AssignedLabel        = metadata.AssignedLabel
	CliqueLabel          = metadata.CliqueLabel
	AssignmentAnnotation = metadata.AssignmentAnnotation

	AssignmentVersion = 1
)

// ObjectReference identifies an exact inventory or rack instance.
type ObjectReference struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

// ProfileReference identifies the rendered profile revision.
type ProfileReference struct {
	Name     string    `json:"name"`
	UID      types.UID `json:"uid"`
	Revision string    `json:"revision"`
}

// Assignment is the versioned Node annotation derived from one exact slot.
type Assignment struct {
	Version   int              `json:"v"`
	Inventory ObjectReference  `json:"inventory"`
	Rack      ObjectReference  `json:"rack"`
	Profile   ProfileReference `json:"profile"`
	RackGroup string           `json:"rackGroup"`
	RackIndex int32            `json:"rackIndex"`
	SlotIndex int32            `json:"slotIndex"`
	NodeUID   types.UID        `json:"nodeUID"`
}

// State describes the last projection attempt for an exact binding.
type State string

//nolint:revive // State values form one closed projection outcome vocabulary.
const (
	// StateProjected indicates that the exact binding is visible on its Node.
	StateProjected State = "Projected"
	StateCleaned   State = "Cleaned"
	StateAbsent    State = "Absent"
	StateConflict  State = "Conflict"
	StateError     State = "Error"
)

//nolint:revive // Reason values form one closed projection diagnostic vocabulary.
const (
	// ReasonProjected records a successful projection attempt.
	ReasonProjected            = "Projected"
	ReasonCleaned              = "Cleaned"
	ReasonExactNodeAbsent      = "ExactNodeAbsent"
	ReasonNodeMetadataConflict = "NodeMetadataConflict"
	ReasonDuplicateBinding     = "DuplicateBinding"
	ReasonProjectionError      = "ProjectionError"
)

// Outcome is status input tied to a complete rack-slot-Node identity.
type Outcome struct {
	InventoryName string
	InventoryUID  types.UID
	RackGroup     string
	RackName      string
	RackUID       types.UID
	RackIndex     int32
	SlotIndex     int32
	NodeName      string
	NodeUID       types.UID
	State         State
	Reason        string
	Message       string
}

// MetadataConflictError reports a value or owner that the controller will not
// replace or share.
type MetadataConflictError struct {
	NodeName string
	Fields   []string
}

func (e *MetadataConflictError) Error() string {
	return fmt.Sprintf("Node %q has conflicting controller metadata fields %v", e.NodeName, e.Fields)
}

// Cache is the informer-backed read surface needed for one projection.
type Cache interface {
	Node(string) (*corev1.Node, error)
	Rack(string) (*mokkav1alpha1.SGPURack, error)
	RacksByNodeUID(types.UID) ([]*mokkav1alpha1.SGPURack, error)
}

// NodePatcher is implemented by the typed core Node client.
type NodePatcher interface {
	Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.Node, error)
}

// Controller applies projections and tracks exact cleanup acknowledgements.
type Controller struct {
	cache   Cache
	patcher NodePatcher

	mu                  sync.RWMutex
	outcomes            map[bindingKey]Outcome
	outcomeByCoordinate map[coordinateKey]bindingKey
	outcomesByInventory map[objectKey]map[bindingKey]struct{}
	outcomesByRack      map[objectKey]map[bindingKey]struct{}
	cleaned             map[bindingKey]struct{}
	cleanedByCoordinate map[coordinateKey]bindingKey
	cleanupBlocks       map[bindingKey]cleanupBlock
	blocksByCoordinate  map[coordinateKey]bindingKey
	// Fixed shards serialize one coordinate without growing lock state with cluster churn.
	operations [operationShards]sync.Mutex
}

var _ controllerack.CleanupGate = (*Controller)(nil)

type bindingKey struct {
	inventoryName string
	inventoryUID  types.UID
	rackGroup     string
	rackName      string
	rackUID       types.UID
	rackIndex     int32
	slotIndex     int32
	nodeName      string
	nodeUID       types.UID
}

type coordinateKey struct {
	rackName  string
	slotIndex int32
}

type objectKey struct {
	name string
	uid  types.UID
}

type outcomeSnapshot struct {
	outcomes []Outcome
	visited  int
}

type cleanupBlock struct {
	assignment string
	before     string
	after      string
}

// NewController builds a Node metadata projector over informer-backed reads.
func NewController(cache Cache, patcher NodePatcher) *Controller {
	return &Controller{
		cache: cache, patcher: patcher,
		outcomes:            make(map[bindingKey]Outcome),
		outcomeByCoordinate: make(map[coordinateKey]bindingKey),
		outcomesByInventory: make(map[objectKey]map[bindingKey]struct{}),
		outcomesByRack:      make(map[objectKey]map[bindingKey]struct{}),
		cleaned:             make(map[bindingKey]struct{}),
		cleanedByCoordinate: make(map[coordinateKey]bindingKey),
		cleanupBlocks:       make(map[bindingKey]cleanupBlock),
		blocksByCoordinate:  make(map[coordinateKey]bindingKey),
	}
}

// Project reconciles metadata for one exact cached rack slot.
func (c *Controller) Project(ctx context.Context, rackName string, slotIndex int32) (Outcome, error) {
	return c.project(ctx, rackName, slotIndex, false)
}

// ProjectFresh reconciles a newly observed binding, superseding a cleanup
// acknowledgement for an older observation of the same exact coordinate.
func (c *Controller) ProjectFresh(ctx context.Context, rackName string, slotIndex int32) (Outcome, error) {
	return c.project(ctx, rackName, slotIndex, true)
}

//nolint:cyclop // Exact-identity projection has distinct stale, conflict, duplicate, and success outcomes.
func (c *Controller) project(ctx context.Context, rackName string, slotIndex int32, fresh bool) (Outcome, error) {
	operation := c.operationLock(rackName, slotIndex)
	operation.Lock()
	defer operation.Unlock()

	rack, err := c.cache.Rack(rackName)
	if err != nil {
		return Outcome{}, fmt.Errorf("get rack %q: %w", rackName, err)
	}
	slot := findSlot(rack, slotIndex)
	if slot == nil || slot.NodeRef == nil {
		return Outcome{}, fmt.Errorf("rack %q slot %d has no binding", rackName, slotIndex)
	}
	outcome := outcomeFor(rack, slot)
	if !fresh && c.cleanupReady(outcome) {
		outcome.State, outcome.Reason = StateCleaned, ReasonCleaned
		return outcome, nil
	}
	c.beginProjection(rack, slot)

	duplicates, err := c.duplicateBindings(slot.NodeRef.UID)
	if err != nil {
		return c.fail(outcome, err)
	}
	if duplicates > 1 {
		err := fmt.Errorf("Node UID %q is bound to %d rack slots", slot.NodeRef.UID, duplicates)
		outcome.State, outcome.Reason, outcome.Message = StateConflict, ReasonDuplicateBinding, err.Error()
		c.record(outcome)
		return outcome, err
	}

	node, err := c.cache.Node(slot.NodeRef.Name)
	if apierrors.IsNotFound(err) {
		outcome.State, outcome.Reason = StateAbsent, ReasonExactNodeAbsent
		c.record(outcome)
		return outcome, nil
	}
	if err != nil {
		return c.fail(outcome, fmt.Errorf("get Node %q: %w", slot.NodeRef.Name, err))
	}
	if node.UID != slot.NodeRef.UID {
		outcome.State, outcome.Reason = StateAbsent, ReasonExactNodeAbsent
		c.record(outcome)
		return outcome, nil
	}

	assignment, err := EncodeAssignment(rack, slot)
	if err != nil {
		return c.fail(outcome, err)
	}
	labels, incompatible := projectionLabels(node, rack)
	if current := node.Annotations[AssignmentAnnotation]; current != "" && current != assignment {
		decoded, decodeErr := DecodeAssignment(current)
		if decodeErr != nil || !assignmentMatches(decoded, rack, slot) {
			incompatible = append(incompatible, AssignmentAnnotation)
		}
	}
	incompatible = append(incompatible, foreignOwnedFields(node, projectionManagedFields(labels))...)
	if len(incompatible) > 0 {
		return c.conflict(outcome, node.Name, incompatible)
	}
	if projectionIsCurrent(node, labels, assignment) && projectionFieldsOwned(node, labels) {
		outcome.State, outcome.Reason = StateProjected, ReasonProjected
		c.record(outcome)
		return outcome, nil
	}

	payload, err := nodeApplyPayload(node.Name, node.UID, labels, map[string]any{AssignmentAnnotation: assignment})
	if err != nil {
		return c.fail(outcome, err)
	}
	response, err := c.patcher.Patch(ctx, node.Name, types.ApplyPatchType, payload, applyOptions())
	if err != nil {
		outcome.Message = err.Error()
		if apierrors.IsConflict(err) {
			outcome.State, outcome.Reason = StateConflict, ReasonNodeMetadataConflict
		} else {
			outcome.State, outcome.Reason = StateError, ReasonProjectionError
		}
		c.record(outcome)
		return outcome, err
	}
	if response == nil {
		return c.fail(outcome, fmt.Errorf("apply Node %q returned an empty response", node.Name))
	}
	if response.Name != node.Name || response.UID != node.UID {
		return c.fail(outcome, fmt.Errorf(
			"apply Node %q returned identity %q/%q, expected %q/%q",
			node.Name, response.Name, response.UID, node.Name, node.UID,
		))
	}
	if conflicts := projectionResponseConflicts(response, labels, assignment); len(conflicts) > 0 {
		return c.conflict(outcome, node.Name, conflicts)
	}
	outcome.State, outcome.Reason = StateProjected, ReasonProjected
	c.record(outcome)
	return outcome, nil
}

// Cleanup removes only compatible controller keys while the assignment still
// identifies the exact binding being retired.
//
//nolint:cyclop // Cleanup deliberately distinguishes deletion, replacement, ownership, and partial progress.
func (c *Controller) Cleanup(ctx context.Context, needed controllerack.CleanupNeeded) (Outcome, error) {
	operation := c.operationLock(needed.RackName, needed.Binding.Coordinate.SlotIndex)
	operation.Lock()
	defer operation.Unlock()

	rack, rackErr := c.cache.Rack(needed.RackName)
	if rackErr != nil && !apierrors.IsNotFound(rackErr) {
		return Outcome{}, fmt.Errorf("get rack %q: %w", needed.RackName, rackErr)
	}
	exactBindingPresent := rackErr == nil && cleanupBindingMatchesRack(needed, rack)
	if !exactBindingPresent {
		rack = nil
	}
	outcome := cleanupOutcome(needed)
	node, err := c.cache.Node(needed.Binding.Node.Name)
	if apierrors.IsNotFound(err) {
		return c.completeCleanup(needed, outcome, ReasonExactNodeAbsent, exactBindingPresent), nil
	}
	if err != nil {
		return c.failCleanup(outcome, fmt.Errorf("get Node %q: %w", needed.Binding.Node.Name, err), exactBindingPresent)
	}
	if node.UID != needed.Binding.Node.UID {
		return c.completeCleanup(needed, outcome, ReasonExactNodeAbsent, exactBindingPresent), nil
	}
	if fields, blocked := c.blockedCleanup(needed, node); blocked {
		outcome, conflict := conflictOutcome(outcome, node.Name, fields)
		c.recordCleanupFailure(outcome, exactBindingPresent)
		return outcome, conflict
	}

	encoded := node.Annotations[AssignmentAnnotation]
	assignment, decodeErr := DecodeAssignment(encoded)
	if encoded == "" || decodeErr != nil || !cleanupAssignmentMatches(assignment, needed) {
		return c.completeCleanup(needed, outcome, ReasonCleaned, exactBindingPresent), nil
	}

	incompatible := make([]string, 0, 2)
	if value, exists := node.Labels[AssignedLabel]; exists {
		if value != "true" {
			incompatible = append(incompatible, AssignedLabel)
		}
	}
	if value, exists := node.Labels[CliqueLabel]; exists {
		expected, hasExpected := cliqueValue(rack)
		if hasExpected && value != expected {
			incompatible = append(incompatible, CliqueLabel)
		}
	}

	annotations := map[string]any(nil)
	if len(incompatible) > 0 {
		annotations = map[string]any{AssignmentAnnotation: encoded}
	}
	released := []managedMetadataField{
		{section: "labels", key: AssignedLabel},
		{section: "labels", key: CliqueLabel},
	}
	if len(annotations) == 0 {
		released = append(released, managedMetadataField{section: "annotations", key: AssignmentAnnotation})
	}
	if len(mokkaOwnedFields(node, released)) == 0 {
		fields := incompatible
		if len(fields) == 0 {
			fields = retainedCleanupFields(node, encoded)
		}
		c.blockCleanup(needed, node, node, encoded)
		outcome, conflict := conflictOutcome(outcome, node.Name, fields)
		c.recordCleanupFailure(outcome, exactBindingPresent)
		return outcome, conflict
	}
	payload, err := nodeApplyPayload(node.Name, node.UID, nil, annotations)
	if err != nil {
		return c.failCleanup(outcome, err, exactBindingPresent)
	}
	response, err := c.patcher.Patch(ctx, node.Name, types.ApplyPatchType, payload, applyOptions())
	if err != nil {
		outcome.Message = err.Error()
		if apierrors.IsConflict(err) {
			outcome.State, outcome.Reason = StateConflict, ReasonNodeMetadataConflict
		} else {
			outcome.State, outcome.Reason = StateError, ReasonProjectionError
		}
		c.recordCleanupFailure(outcome, exactBindingPresent)
		return outcome, err
	}
	if response == nil {
		return c.failCleanup(outcome, fmt.Errorf("apply Node %q cleanup returned an empty response", node.Name), exactBindingPresent)
	}
	if response.Name != node.Name {
		return c.failCleanup(outcome, fmt.Errorf(
			"apply Node %q cleanup returned Node %q", node.Name, response.Name,
		), exactBindingPresent)
	}
	if response.UID != node.UID {
		return c.completeCleanup(needed, outcome, ReasonExactNodeAbsent, exactBindingPresent), nil
	}
	if retained := retainedCleanupFields(response, encoded); len(retained) > 0 {
		blockedAfter := response
		if len(incompatible) > 0 && len(retained) == 1 && retained[0] == AssignmentAnnotation {
			blockedAfter = nil
		}
		c.blockCleanup(needed, node, blockedAfter, encoded)
		outcome, conflict := conflictOutcome(outcome, response.Name, retained)
		c.recordCleanupFailure(outcome, exactBindingPresent)
		return outcome, conflict
	}
	return c.completeCleanup(needed, outcome, ReasonCleaned, exactBindingPresent), nil
}

// Ready reports whether an exact cleanup acknowledgement is pending.
func (c *Controller) Ready(needed controllerack.CleanupNeeded) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ready := c.cleaned[bindingKeyForCleanup(needed)]
	return ready
}

// Outcomes returns a deterministic immutable status snapshot.
func (c *Controller) Outcomes() []Outcome {
	c.mu.RLock()
	defer c.mu.RUnlock()
	outcomes := make([]Outcome, 0, len(c.outcomes))
	for _, outcome := range c.outcomes {
		outcomes = append(outcomes, outcome)
	}
	sortOutcomes(outcomes)
	return outcomes
}

// OutcomesForInventory returns status state for one exact inventory instance.
func (c *Controller) OutcomesForInventory(name string, uid types.UID) []Outcome {
	return c.snapshotForInventory(name, uid).outcomes
}

// OutcomesForRack returns status state for one exact rack instance.
func (c *Controller) OutcomesForRack(name string, uid types.UID) []Outcome {
	return c.snapshotForRack(name, uid).outcomes
}

func (c *Controller) snapshotForInventory(name string, uid types.UID) outcomeSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked(c.outcomesByInventory[objectKey{name: name, uid: uid}])
}

func (c *Controller) snapshotForRack(name string, uid types.UID) outcomeSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked(c.outcomesByRack[objectKey{name: name, uid: uid}])
}

func (c *Controller) snapshotLocked(keys map[bindingKey]struct{}) outcomeSnapshot {
	outcomes := make([]Outcome, 0, len(keys))
	visited := 0
	for key := range keys {
		visited++
		if outcome, exists := c.outcomes[key]; exists {
			outcomes = append(outcomes, outcome)
		}
	}
	sortOutcomes(outcomes)
	return outcomeSnapshot{outcomes: outcomes, visited: visited}
}

func sortOutcomes(outcomes []Outcome) {
	slices.SortFunc(outcomes, func(a, b Outcome) int {
		if order := cmp.Compare(a.RackName, b.RackName); order != 0 {
			return order
		}
		if order := cmp.Compare(a.SlotIndex, b.SlotIndex); order != 0 {
			return order
		}
		return cmp.Compare(string(a.NodeUID), string(b.NodeUID))
	})
}

// EncodeAssignment serializes the compact annotation without whitespace.
func EncodeAssignment(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) (string, error) {
	if rack == nil || rack.Name == "" || rack.UID == "" || slot == nil || slot.NodeRef == nil || slot.NodeRef.UID == "" {
		return "", errors.New("assignment requires exact rack, slot, and node identities")
	}
	encoded, err := json.Marshal(Assignment{
		Version:   AssignmentVersion,
		Inventory: ObjectReference{Name: rack.Spec.InventoryRef.Name, UID: rack.Spec.InventoryRef.UID},
		Rack:      ObjectReference{Name: rack.Name, UID: rack.UID},
		Profile:   ProfileReference{Name: rack.Spec.ProfileRef.Name, UID: rack.Spec.ProfileRef.UID, Revision: rack.Spec.ProfileRef.Revision},
		RackGroup: rack.Spec.Identity.RackGroup,
		RackIndex: rack.Spec.Identity.RackIndex,
		SlotIndex: slot.Index,
		NodeUID:   slot.NodeRef.UID,
	})
	if err != nil {
		return "", fmt.Errorf("encode Node assignment: %w", err)
	}
	return string(encoded), nil
}

// DecodeAssignment validates the complete compact Node assignment document.
func DecodeAssignment(value string) (Assignment, error) {
	var assignment Assignment
	if err := json.Unmarshal([]byte(value), &assignment); err != nil {
		return Assignment{}, fmt.Errorf("decode Node assignment: %w", err)
	}
	if assignment.Version != AssignmentVersion {
		return Assignment{}, fmt.Errorf("unsupported Node assignment version %d", assignment.Version)
	}
	return assignment, nil
}

// MatchesBinding reports whether a Node already carries the exact projection
// derived from a durable rack binding.
//
//nolint:cyclop // The predicate intentionally checks every exact identity component.
func MatchesBinding(node *corev1.Node, rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) bool {
	if node == nil || rack == nil || slot == nil || slot.NodeRef == nil ||
		node.Name != slot.NodeRef.Name || node.UID != slot.NodeRef.UID ||
		node.Labels[AssignedLabel] != "true" {
		return false
	}
	clique, hasClique := cliqueValue(rack)
	if hasClique && node.Labels[CliqueLabel] != clique {
		return false
	}
	if !hasClique && node.Labels[CliqueLabel] != "" {
		return false
	}
	assignment, err := DecodeAssignment(node.Annotations[AssignmentAnnotation])
	if err != nil || !assignmentMatches(assignment, rack, slot) {
		return false
	}
	projectionLabels := map[string]any{AssignedLabel: "true"}
	if hasClique {
		projectionLabels[CliqueLabel] = clique
	}
	return len(foreignOwnedFields(node, projectionManagedFields(projectionLabels))) == 0 &&
		projectionFieldsOwned(node, projectionLabels)
}

func projectionLabels(node *corev1.Node, rack *mokkav1alpha1.SGPURack) (map[string]any, []string) {
	labels := map[string]any{AssignedLabel: "true"}
	incompatible := make([]string, 0, 2)
	if value, exists := node.Labels[AssignedLabel]; exists && value != "true" {
		incompatible = append(incompatible, AssignedLabel)
	}
	if clique, hasClique := cliqueValue(rack); hasClique {
		labels[CliqueLabel] = clique
		if value, exists := node.Labels[CliqueLabel]; exists && value != clique {
			incompatible = append(incompatible, CliqueLabel)
		}
	} else if _, exists := node.Labels[CliqueLabel]; exists {
		current, err := DecodeAssignment(node.Annotations[AssignmentAnnotation])
		slot := findSlotByUID(rack, node.UID)
		if err == nil && slot != nil && assignmentMatches(current, rack, slot) {
			labels[CliqueLabel] = nil
		} else {
			incompatible = append(incompatible, CliqueLabel)
		}
	}
	return labels, incompatible
}

func projectionIsCurrent(node *corev1.Node, labels map[string]any, assignment string) bool {
	if node.Annotations[AssignmentAnnotation] != assignment {
		return false
	}
	for key, desired := range labels {
		current, exists := node.Labels[key]
		if desired == nil {
			if exists {
				return false
			}
			continue
		}
		desiredValue, ok := desired.(string)
		if !ok || !exists || current != desiredValue {
			return false
		}
	}
	return true
}

func projectionFieldsOwned(node *corev1.Node, labels map[string]any) bool {
	return len(missingMokkaOwnership(node, projectionPresentFields(labels))) == 0
}

type managedMetadataField struct {
	section string
	key     string
}

func (f managedMetadataField) path() []string {
	return []string{"f:metadata", "f:" + f.section, "f:" + f.key}
}

func projectionManagedFields(labels map[string]any) []managedMetadataField {
	fields := make([]managedMetadataField, 0, 1+len(labels))
	fields = append(fields, managedMetadataField{section: "annotations", key: AssignmentAnnotation})
	for key := range labels {
		fields = append(fields, managedMetadataField{section: "labels", key: key})
	}
	return fields
}

func projectionPresentFields(labels map[string]any) []managedMetadataField {
	fields := make([]managedMetadataField, 0, 1+len(labels))
	fields = append(fields, managedMetadataField{section: "annotations", key: AssignmentAnnotation})
	for key, value := range labels {
		if value != nil {
			fields = append(fields, managedMetadataField{section: "labels", key: key})
		}
	}
	return fields
}

func projectionResponseConflicts(node *corev1.Node, labels map[string]any, assignment string) []string {
	conflicts := projectionValueConflicts(node, labels, assignment)
	wanted := projectionManagedFields(labels)
	conflicts = append(conflicts, foreignOwnedFields(node, wanted)...)
	conflicts = append(conflicts, missingMokkaOwnership(node, projectionPresentFields(labels))...)
	return sortedUnique(conflicts)
}

func projectionValueConflicts(node *corev1.Node, labels map[string]any, assignment string) []string {
	conflicts := make([]string, 0, len(labels)+1)
	if node.Annotations[AssignmentAnnotation] != assignment {
		conflicts = append(conflicts, AssignmentAnnotation)
	}
	for key, desired := range labels {
		current, exists := node.Labels[key]
		if desired == nil {
			if exists {
				conflicts = append(conflicts, key)
			}
			continue
		}
		value, ok := desired.(string)
		if !ok || !exists || current != value {
			conflicts = append(conflicts, key)
		}
	}
	return conflicts
}

func foreignOwnedFields(node *corev1.Node, fields []managedMetadataField) []string {
	conflicts := make([]string, 0)
	for _, field := range fields {
		for _, entry := range node.ManagedFields {
			if entry.Manager == FieldManager || entry.Subresource != "" || entry.FieldsType != "FieldsV1" || entry.FieldsV1 == nil {
				continue
			}
			if fieldsV1Owns(entry.FieldsV1.GetRawBytes(), field.path()) {
				conflicts = append(conflicts, field.key)
				break
			}
		}
	}
	return sortedUnique(conflicts)
}

func missingMokkaOwnership(node *corev1.Node, fields []managedMetadataField) []string {
	missing := make([]string, 0)
	for _, field := range fields {
		if !mokkaOwnsField(node.ManagedFields, field) {
			missing = append(missing, field.key)
		}
	}
	return sortedUnique(missing)
}

func mokkaOwnsField(entries []metav1.ManagedFieldsEntry, field managedMetadataField) bool {
	for _, entry := range entries {
		if entry.Manager != FieldManager || entry.Operation != metav1.ManagedFieldsOperationApply ||
			entry.APIVersion != "v1" || entry.Subresource != "" || entry.FieldsType != "FieldsV1" || entry.FieldsV1 == nil {
			continue
		}
		if fieldsV1Owns(entry.FieldsV1.GetRawBytes(), field.path()) {
			return true
		}
	}
	return false
}

func mokkaOwnedFields(node *corev1.Node, fields []managedMetadataField) []string {
	owned := make([]string, 0)
	for _, field := range fields {
		if len(missingMokkaOwnership(node, []managedMetadataField{field})) == 0 {
			owned = append(owned, field.key)
		}
	}
	return sortedUnique(owned)
}

// CompactManagedFields retains only ownership of projection metadata keys.
// The informer does not need the rest of a Node's potentially large field set.
func CompactManagedFields(entries []metav1.ManagedFieldsEntry) []metav1.ManagedFieldsEntry {
	relevant := []managedMetadataField{
		{section: "annotations", key: AssignmentAnnotation},
		{section: "labels", key: AssignedLabel},
		{section: "labels", key: CliqueLabel},
	}
	compacted := make([]metav1.ManagedFieldsEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.FieldsType != "FieldsV1" || entry.FieldsV1 == nil {
			continue
		}
		fields := make(map[string]map[string]any)
		for _, field := range relevant {
			if !fieldsV1Owns(entry.FieldsV1.GetRawBytes(), field.path()) {
				continue
			}
			section := "f:" + field.section
			if fields[section] == nil {
				fields[section] = make(map[string]any)
			}
			fields[section]["f:"+field.key] = map[string]any{}
		}
		if len(fields) == 0 {
			continue
		}
		raw, err := json.Marshal(map[string]any{"f:metadata": fields})
		if err != nil {
			continue
		}
		compactedEntry := entry
		compactedEntry.Time = nil
		compactedEntry.FieldsV1 = metav1.NewFieldsV1(string(raw))
		compacted = append(compacted, compactedEntry)
	}
	return compacted
}

func fieldsV1Owns(raw []byte, path []string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for index, key := range path {
		child, exists := fields[key]
		if !exists {
			return false
		}
		if index == len(path)-1 {
			return true
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(child, &nested); err != nil {
			return false
		}
		fields = nested
	}
	return false
}

func retainedCleanupFields(node *corev1.Node, assignment string) []string {
	retained := make([]string, 0, 3)
	if node.Annotations[AssignmentAnnotation] == assignment {
		retained = append(retained, AssignmentAnnotation)
	}
	if _, exists := node.Labels[AssignedLabel]; exists {
		retained = append(retained, AssignedLabel)
	}
	if _, exists := node.Labels[CliqueLabel]; exists {
		retained = append(retained, CliqueLabel)
	}
	return sortedUnique(retained)
}

func sortedUnique(fields []string) []string {
	slices.Sort(fields)
	return slices.Compact(fields)
}

func nodeApplyPayload(name string, uid types.UID, labels, annotations map[string]any) ([]byte, error) {
	metadata := map[string]any{"name": name, "uid": uid}
	if len(labels) > 0 {
		metadata["labels"] = labels
	}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	payload, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Node apply patch: %w", err)
	}
	return payload, nil
}

func applyOptions() metav1.PatchOptions {
	return metav1.PatchOptions{FieldManager: FieldManager, Force: ptr.To(false)}
}

func cliqueValue(rack *mokkav1alpha1.SGPURack) (string, bool) {
	if rack == nil || rack.Spec.GPUFabric == nil {
		return "", false
	}
	return fmt.Sprintf("%s.%d", rack.Spec.Identity.FabricUUID, rack.Spec.Identity.CliqueID), true
}

func assignmentMatches(assignment Assignment, rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) bool {
	return assignment.Version == AssignmentVersion && rack != nil && slot != nil && slot.NodeRef != nil &&
		assignment.Inventory == (ObjectReference{Name: rack.Spec.InventoryRef.Name, UID: rack.Spec.InventoryRef.UID}) &&
		assignment.Rack == (ObjectReference{Name: rack.Name, UID: rack.UID}) &&
		assignment.RackGroup == rack.Spec.Identity.RackGroup && assignment.RackIndex == rack.Spec.Identity.RackIndex &&
		assignment.SlotIndex == slot.Index && assignment.NodeUID == slot.NodeRef.UID
}

func cleanupAssignmentMatches(assignment Assignment, needed controllerack.CleanupNeeded) bool {
	binding := needed.Binding
	if assignment.Version != AssignmentVersion || assignment.Inventory.Name != binding.Coordinate.Group.InventoryName ||
		assignment.Inventory.UID != binding.Coordinate.Group.InventoryUID || assignment.Rack.Name != needed.RackName ||
		assignment.Rack.UID != needed.RackUID ||
		assignment.RackGroup != binding.Coordinate.Group.RackGroup || assignment.RackIndex != binding.Coordinate.RackIndex ||
		assignment.SlotIndex != binding.Coordinate.SlotIndex || assignment.NodeUID != binding.Node.UID {
		return false
	}
	return true
}

//nolint:cyclop // Cleanup acknowledgement requires every object and coordinate identity to match.
func cleanupBindingMatchesRack(needed controllerack.CleanupNeeded, rack *mokkav1alpha1.SGPURack) bool {
	binding := needed.Binding
	if rack == nil || rack.Name != needed.RackName || rack.UID != needed.RackUID ||
		rack.Spec.InventoryRef.Name != binding.Coordinate.Group.InventoryName ||
		rack.Spec.InventoryRef.UID != binding.Coordinate.Group.InventoryUID ||
		rack.Spec.Identity.RackGroup != binding.Coordinate.Group.RackGroup ||
		rack.Spec.Identity.RackIndex != binding.Coordinate.RackIndex {
		return false
	}
	slot := findSlot(rack, binding.Coordinate.SlotIndex)
	return slot != nil && slot.NodeRef != nil && slot.NodeRef.Name == binding.Node.Name && slot.NodeRef.UID == binding.Node.UID
}

func (c *Controller) duplicateBindings(uid types.UID) (int, error) {
	racks, err := c.cache.RacksByNodeUID(uid)
	if err != nil {
		return 0, fmt.Errorf("get racks for Node UID %q: %w", uid, err)
	}
	count := 0
	for _, rack := range racks {
		for _, slot := range rack.Spec.Slots {
			if slot.NodeRef != nil && slot.NodeRef.UID == uid {
				count++
			}
		}
	}
	return count, nil
}

func findSlot(rack *mokkav1alpha1.SGPURack, index int32) *mokkav1alpha1.SGPURackSlot {
	for i := range rack.Spec.Slots {
		if rack.Spec.Slots[i].Index == index {
			return &rack.Spec.Slots[i]
		}
	}
	return nil
}

func findSlotByUID(rack *mokkav1alpha1.SGPURack, uid types.UID) *mokkav1alpha1.SGPURackSlot {
	for i := range rack.Spec.Slots {
		if rack.Spec.Slots[i].NodeRef != nil && rack.Spec.Slots[i].NodeRef.UID == uid {
			return &rack.Spec.Slots[i]
		}
	}
	return nil
}

func outcomeFor(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) Outcome {
	return Outcome{
		InventoryName: rack.Spec.InventoryRef.Name, InventoryUID: rack.Spec.InventoryRef.UID,
		RackGroup: rack.Spec.Identity.RackGroup, RackName: rack.Name, RackUID: rack.UID,
		RackIndex: rack.Spec.Identity.RackIndex, SlotIndex: slot.Index,
		NodeName: slot.NodeRef.Name, NodeUID: slot.NodeRef.UID,
	}
}

func cleanupOutcome(needed controllerack.CleanupNeeded) Outcome {
	binding := needed.Binding
	return Outcome{
		InventoryName: binding.Coordinate.Group.InventoryName, InventoryUID: binding.Coordinate.Group.InventoryUID,
		RackGroup: binding.Coordinate.Group.RackGroup, RackName: needed.RackName, RackUID: needed.RackUID,
		RackIndex: binding.Coordinate.RackIndex, SlotIndex: binding.Coordinate.SlotIndex,
		NodeName: needed.Binding.Node.Name, NodeUID: needed.Binding.Node.UID,
	}
}

func (c *Controller) record(outcome Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putOutcomeLocked(outcome)
}

func (c *Controller) fail(outcome Outcome, err error) (Outcome, error) {
	outcome.State, outcome.Reason, outcome.Message = StateError, ReasonProjectionError, err.Error()
	c.record(outcome)
	return outcome, err
}

func (c *Controller) conflict(outcome Outcome, nodeName string, fields []string) (Outcome, error) {
	outcome, conflict := conflictOutcome(outcome, nodeName, fields)
	c.record(outcome)
	return outcome, conflict
}

func conflictOutcome(outcome Outcome, nodeName string, fields []string) (Outcome, *MetadataConflictError) {
	conflict := &MetadataConflictError{NodeName: nodeName, Fields: sortedUnique(fields)}
	outcome.State, outcome.Reason, outcome.Message = StateConflict, ReasonNodeMetadataConflict, conflict.Error()
	return outcome, conflict
}

func (c *Controller) failCleanup(outcome Outcome, err error, exactRackPresent bool) (Outcome, error) {
	outcome.State, outcome.Reason, outcome.Message = StateError, ReasonProjectionError, err.Error()
	c.recordCleanupFailure(outcome, exactRackPresent)
	return outcome, err
}

func (c *Controller) recordCleanupFailure(outcome Outcome, exactRackPresent bool) {
	if exactRackPresent {
		c.record(outcome)
	}
}

func (c *Controller) completeCleanup(
	needed controllerack.CleanupNeeded,
	outcome Outcome,
	reason string,
	exactRackPresent bool,
) Outcome {
	outcome.State, outcome.Reason = StateCleaned, reason
	c.mu.Lock()
	key := bindingKeyForCleanup(needed)
	c.deleteCleanupBlockLocked(key)
	c.deleteOutcomeLocked(key)
	coordinate := coordinateKeyForBinding(key)
	if exactRackPresent {
		if previous, exists := c.cleanedByCoordinate[coordinate]; exists && previous != key {
			delete(c.cleaned, previous)
		}
		c.cleaned[key] = struct{}{}
		c.cleanedByCoordinate[coordinate] = key
	} else {
		delete(c.cleaned, key)
		if c.cleanedByCoordinate[coordinate] == key {
			delete(c.cleanedByCoordinate, coordinate)
		}
	}
	c.mu.Unlock()
	return outcome
}

func (c *Controller) beginProjection(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	coordinate := coordinateKey{rackName: rack.Name, slotIndex: slot.Index}
	current := bindingKeyForOutcome(outcomeFor(rack, slot))
	if blocked, exists := c.blocksByCoordinate[coordinate]; exists && blocked != current {
		c.deleteCleanupBlockLocked(blocked)
	}
	if key, exists := c.outcomeByCoordinate[coordinate]; exists {
		c.deleteOutcomeLocked(key)
	}
	if key, exists := c.cleanedByCoordinate[coordinate]; exists {
		delete(c.cleaned, key)
		delete(c.cleanedByCoordinate, coordinate)
	}
}

func (c *Controller) blockedCleanup(needed controllerack.CleanupNeeded, node *corev1.Node) ([]string, bool) {
	c.mu.RLock()
	block, exists := c.cleanupBlocks[bindingKeyForCleanup(needed)]
	c.mu.RUnlock()
	if !exists {
		return nil, false
	}
	fields := retainedCleanupFields(node, block.assignment)
	if len(fields) == 0 {
		return nil, false
	}
	observation := cleanupObservation(node)
	if observation != block.before && observation != block.after &&
		node.Annotations[AssignmentAnnotation] == block.assignment {
		return nil, false
	}
	return fields, true
}

func (c *Controller) blockCleanup(
	needed controllerack.CleanupNeeded,
	before, after *corev1.Node,
	assignment string,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := bindingKeyForCleanup(needed)
	coordinate := coordinateKeyForBinding(key)
	if previous, exists := c.blocksByCoordinate[coordinate]; exists && previous != key {
		c.deleteCleanupBlockLocked(previous)
	}
	c.cleanupBlocks[key] = cleanupBlock{
		assignment: assignment,
		before:     cleanupObservation(before),
		after:      cleanupObservation(after),
	}
	c.blocksByCoordinate[coordinate] = key
}

func (c *Controller) deleteCleanupBlockLocked(key bindingKey) {
	delete(c.cleanupBlocks, key)
	coordinate := coordinateKeyForBinding(key)
	if c.blocksByCoordinate[coordinate] == key {
		delete(c.blocksByCoordinate, coordinate)
	}
}

func cleanupObservation(node *corev1.Node) string {
	if node == nil {
		return ""
	}
	labels := make(map[string]string, 2)
	for _, key := range []string{AssignedLabel, CliqueLabel} {
		if value, exists := node.Labels[key]; exists {
			labels[key] = value
		}
	}
	annotations := make(map[string]string, 1)
	if value, exists := node.Annotations[AssignmentAnnotation]; exists {
		annotations[AssignmentAnnotation] = value
	}
	encoded, err := json.Marshal(struct {
		UID           types.UID                   `json:"uid"`
		Labels        map[string]string           `json:"labels,omitempty"`
		Annotations   map[string]string           `json:"annotations,omitempty"`
		ManagedFields []metav1.ManagedFieldsEntry `json:"managedFields,omitempty"`
	}{
		UID: node.UID, Labels: labels, Annotations: annotations,
		ManagedFields: CompactManagedFields(node.ManagedFields),
	})
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (c *Controller) putOutcomeLocked(outcome Outcome) {
	key := bindingKeyForOutcome(outcome)
	coordinate := coordinateKeyForBinding(key)
	if previous, exists := c.outcomeByCoordinate[coordinate]; exists && previous != key {
		c.deleteOutcomeLocked(previous)
	}
	c.outcomes[key] = outcome
	c.outcomeByCoordinate[coordinate] = key
	addOutcomeIndex(c.outcomesByInventory, objectKey{name: outcome.InventoryName, uid: outcome.InventoryUID}, key)
	addOutcomeIndex(c.outcomesByRack, objectKey{name: outcome.RackName, uid: outcome.RackUID}, key)
}

func (c *Controller) deleteOutcomeLocked(key bindingKey) {
	outcome, exists := c.outcomes[key]
	if !exists {
		return
	}
	delete(c.outcomes, key)
	coordinate := coordinateKeyForBinding(key)
	if c.outcomeByCoordinate[coordinate] == key {
		delete(c.outcomeByCoordinate, coordinate)
	}
	removeOutcomeIndex(c.outcomesByInventory, objectKey{name: outcome.InventoryName, uid: outcome.InventoryUID}, key)
	removeOutcomeIndex(c.outcomesByRack, objectKey{name: outcome.RackName, uid: outcome.RackUID}, key)
}

func addOutcomeIndex(index map[objectKey]map[bindingKey]struct{}, object objectKey, key bindingKey) {
	keys := index[object]
	if keys == nil {
		keys = make(map[bindingKey]struct{})
		index[object] = keys
	}
	keys[key] = struct{}{}
}

func removeOutcomeIndex(index map[objectKey]map[bindingKey]struct{}, object objectKey, key bindingKey) {
	keys := index[object]
	delete(keys, key)
	if len(keys) == 0 {
		delete(index, object)
	}
}

func coordinateKeyForBinding(key bindingKey) coordinateKey {
	return coordinateKey{rackName: key.rackName, slotIndex: key.slotIndex}
}

func (c *Controller) cleanupReady(outcome Outcome) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ready := c.cleaned[bindingKeyForOutcome(outcome)]
	return ready
}

func (c *Controller) operationLock(rackName string, slotIndex int32) *sync.Mutex {
	hash := uint32(2166136261)
	for i := range len(rackName) {
		hash ^= uint32(rackName[i])
		hash *= 16777619
	}
	hash ^= uint32(slotIndex)
	hash *= 16777619
	return &c.operations[hash%operationShards]
}

func bindingKeyForOutcome(outcome Outcome) bindingKey {
	return bindingKey{
		inventoryName: outcome.InventoryName, inventoryUID: outcome.InventoryUID,
		rackGroup: outcome.RackGroup, rackName: outcome.RackName, rackUID: outcome.RackUID,
		rackIndex: outcome.RackIndex, slotIndex: outcome.SlotIndex,
		nodeName: outcome.NodeName, nodeUID: outcome.NodeUID,
	}
}

func bindingKeyForCleanup(needed controllerack.CleanupNeeded) bindingKey {
	binding := needed.Binding
	return bindingKey{
		inventoryName: binding.Coordinate.Group.InventoryName, inventoryUID: binding.Coordinate.Group.InventoryUID,
		rackGroup: binding.Coordinate.Group.RackGroup, rackName: needed.RackName, rackUID: needed.RackUID,
		rackIndex: binding.Coordinate.RackIndex, slotIndex: binding.Coordinate.SlotIndex,
		nodeName: binding.Node.Name, nodeUID: binding.Node.UID,
	}
}
