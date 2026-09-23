// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package projection derives the small controller-owned Node metadata surface
// from durable SGPURack bindings.
package projection

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/allocate"
	sgpuassignment "github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	sgpumetadata "github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	sgpurelease "github.com/NVIDIA/k8s-test-infra/internal/sgpu/release"
)

const (
	// FieldManager owns only the compact metadata derived from rack bindings.
	FieldManager    = "mokka-controller"
	operationShards = 256
)

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
	ReasonBindingNotAllocated  = "BindingNotAllocated"
	ReasonProjectionError      = "ProjectionError"
)

// Outcome is status input tied to a complete rack/logical-Node/Kubernetes-Node identity.
type Outcome struct {
	InventoryName string
	InventoryUID  types.UID
	RackGroup     string
	RackName      string
	RackUID       types.UID
	RackIndex     int32
	NodeIndex     int32
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
	Node(context.Context, string) (*corev1.Node, error)
	ProjectionTarget(*mokkav1alpha1.SGPURack, *mokkav1alpha1.SGPURackNode) (*corev1.Node, bool, error)
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
	cleaned             map[bindingKey]sgpurelease.Reason
	cleanedByCoordinate map[coordinateKey]bindingKey
	cleanupBlocks       map[bindingKey]cleanupBlock
	blocksByCoordinate  map[coordinateKey]bindingKey
	// Fixed shards serialize one coordinate without growing lock state with cluster churn.
	operations [operationShards]sync.Mutex
}

var _ sgpurelease.Gate = (*Controller)(nil)

type bindingKey struct {
	inventoryName string
	inventoryUID  types.UID
	rackGroup     string
	rackName      string
	rackUID       types.UID
	rackIndex     int32
	nodeIndex     int32
	nodeName      string
	nodeUID       types.UID
}

type coordinateKey struct {
	rackName  string
	nodeIndex int32
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
		cleaned:             make(map[bindingKey]sgpurelease.Reason),
		cleanedByCoordinate: make(map[coordinateKey]bindingKey),
		cleanupBlocks:       make(map[bindingKey]cleanupBlock),
		blocksByCoordinate:  make(map[coordinateKey]bindingKey),
	}
}

// Project reconciles metadata for one exact cached logical rack Node.
func (c *Controller) Project(ctx context.Context, rackName string, nodeIndex int32) (Outcome, error) {
	return c.project(ctx, rackName, nodeIndex, false)
}

// ProjectFresh reconciles a newly observed binding, superseding a cleanup
// acknowledgement for an older observation of the same exact coordinate.
func (c *Controller) ProjectFresh(ctx context.Context, rackName string, nodeIndex int32) (Outcome, error) {
	return c.project(ctx, rackName, nodeIndex, true)
}

//nolint:cyclop // Exact-identity projection has distinct stale, conflict, duplicate, and success outcomes.
func (c *Controller) project(ctx context.Context, rackName string, nodeIndex int32, fresh bool) (Outcome, error) {
	operation := c.operationLock(rackName, nodeIndex)
	operation.Lock()
	defer operation.Unlock()

	rack, err := c.cache.Rack(rackName)
	if err != nil {
		return Outcome{}, fmt.Errorf("get rack %q: %w", rackName, err)
	}
	slot := rack.Spec.NodeByIndex(nodeIndex)
	if slot == nil || slot.NodeRef == nil {
		return Outcome{}, fmt.Errorf("rack %q logical Node %d has no binding", rackName, nodeIndex)
	}
	outcome := outcomeFor(rack, slot)
	if !fresh && c.cleanupReady(outcome) {
		outcome.State, outcome.Reason = StateCleaned, ReasonCleaned
		return outcome, nil
	}
	node, allowed, err := c.cache.ProjectionTarget(rack, slot)
	if err != nil {
		return Outcome{}, fmt.Errorf("verify rack %q logical Node %d projection target: %w", rackName, nodeIndex, err)
	}
	if !allowed {
		outcome.State, outcome.Reason = StateAbsent, ReasonBindingNotAllocated
		c.record(outcome)
		return outcome, nil
	}
	c.beginProjection(rack, slot)

	duplicates, err := c.duplicateBindings(slot.NodeRef.UID)
	if err != nil {
		return c.fail(outcome, err)
	}
	if duplicates > 1 {
		err := fmt.Errorf("Node UID %q is bound to %d logical rack Nodes", slot.NodeRef.UID, duplicates)
		outcome.State, outcome.Reason, outcome.Message = StateConflict, ReasonDuplicateBinding, err.Error()
		c.record(outcome)
		return outcome, err
	}

	encodedAssignment, err := sgpuassignment.EncodeAssignment(rack, slot)
	if err != nil {
		return c.fail(outcome, err)
	}
	labels, incompatible := projectionLabels(node, rack)
	if current := node.Annotations[sgpumetadata.AssignmentAnnotation]; current != "" && current != encodedAssignment {
		decoded, decodeErr := sgpuassignment.DecodeAssignment(current)
		if decodeErr != nil || !assignmentMatches(decoded, rack, slot) {
			incompatible = append(incompatible, sgpumetadata.AssignmentAnnotation)
		}
	}
	incompatible = append(incompatible, foreignOwnedFields(node, projectionManagedFields(labels))...)
	if len(incompatible) > 0 {
		return c.conflict(outcome, node.Name, incompatible)
	}
	if projectionIsCurrent(node, labels, encodedAssignment) && projectionFieldsOwned(node, labels) {
		outcome.State, outcome.Reason = StateProjected, ReasonProjected
		c.record(outcome)
		return outcome, nil
	}

	payload, err := nodeApplyPayload(node.Name, node.UID, labels, map[string]any{sgpumetadata.AssignmentAnnotation: encodedAssignment})
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
	if conflicts := projectionResponseConflicts(response, labels, encodedAssignment); len(conflicts) > 0 {
		return c.conflict(outcome, node.Name, conflicts)
	}
	outcome.State, outcome.Reason = StateProjected, ReasonProjected
	c.record(outcome)
	zap.L().Info("Projected sGPU assignment onto Node", outcomeFields(outcome)...)
	return outcome, nil
}

// Cleanup removes only compatible controller keys while the assignment still
// identifies the exact binding being retired.
//
//nolint:cyclop // Cleanup deliberately distinguishes deletion, replacement, ownership, and partial progress.
func (c *Controller) Cleanup(ctx context.Context, needed sgpurelease.Cleanup) (Outcome, error) {
	operation := c.operationLock(needed.RackName, needed.Binding.Coordinate.NodeIndex)
	operation.Lock()
	defer operation.Unlock()

	rack, rackErr := c.cache.Rack(needed.RackName)
	if rackErr != nil && !apierrors.IsNotFound(rackErr) {
		return Outcome{}, fmt.Errorf("get rack %q: %w", needed.RackName, rackErr)
	}
	exactBindingPresent := rackErr == nil && needed.MatchesRack(rack)
	if !exactBindingPresent {
		rack = nil
	}
	outcome := cleanupOutcome(needed)
	node, err := c.cache.Node(ctx, needed.Binding.Node.Name)
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

	encoded := node.Annotations[sgpumetadata.AssignmentAnnotation]
	assignment, decodeErr := sgpuassignment.DecodeAssignment(encoded)
	if encoded == "" || decodeErr != nil || !cleanupAssignmentMatches(assignment, needed) {
		return c.completeCleanup(needed, outcome, ReasonCleaned, exactBindingPresent), nil
	}

	incompatible := make([]string, 0, 2)
	if value, exists := node.Labels[sgpumetadata.AssignedLabel]; exists {
		if value != "true" {
			incompatible = append(incompatible, sgpumetadata.AssignedLabel)
		}
	}
	if value, exists := node.Labels[sgpumetadata.CliqueLabel]; exists {
		expected, hasExpected := cliqueValue(rack)
		if hasExpected && value != expected {
			incompatible = append(incompatible, sgpumetadata.CliqueLabel)
		}
	}

	annotations := map[string]any(nil)
	if len(incompatible) > 0 {
		annotations = map[string]any{sgpumetadata.AssignmentAnnotation: encoded}
	}
	released := []managedMetadataField{
		{section: "labels", key: sgpumetadata.AssignedLabel},
		{section: "labels", key: sgpumetadata.CliqueLabel},
	}
	if len(annotations) == 0 {
		released = append(released, managedMetadataField{section: "annotations", key: sgpumetadata.AssignmentAnnotation})
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
		if len(incompatible) > 0 && len(retained) == 1 && retained[0] == sgpumetadata.AssignmentAnnotation {
			blockedAfter = nil
		}
		c.blockCleanup(needed, node, blockedAfter, encoded)
		outcome, conflict := conflictOutcome(outcome, response.Name, retained)
		c.recordCleanupFailure(outcome, exactBindingPresent)
		return outcome, conflict
	}
	zap.L().Info("Removed sGPU assignment projection from Node",
		append(outcomeFields(outcome), zap.String("reason", string(needed.Reason)))...)
	return c.completeCleanup(needed, outcome, ReasonCleaned, exactBindingPresent), nil
}

// Ready reports whether an exact cleanup acknowledgement is pending.
func (c *Controller) Ready(needed sgpurelease.Cleanup) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ready := c.cleaned[bindingKeyForCleanup(needed)]
	return ready
}

// RevokeCleanup removes only the exact acknowledgement for an obsolete
// allocation decision.
func (c *Controller) RevokeCleanup(needed sgpurelease.Cleanup) {
	operation := c.operationLock(needed.RackName, needed.Binding.Coordinate.NodeIndex)
	operation.Lock()
	defer operation.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	key := bindingKeyForCleanup(needed)
	delete(c.cleaned, key)
	coordinate := coordinateKeyForBinding(key)
	if c.cleanedByCoordinate[coordinate] == key {
		delete(c.cleanedByCoordinate, coordinate)
	}
}

// HasAcknowledgedCleanups avoids scanning retained allocations in the steady
// state where no cleanup handoff is pending.
func (c *Controller) HasAcknowledgedCleanups() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cleaned) > 0
}

// AcknowledgedCleanup returns the exact pending acknowledgement for a durable
// binding, if one exists.
func (c *Controller) AcknowledgedCleanup(
	rackName string,
	binding allocate.Binding,
) (sgpurelease.Cleanup, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	coordinate := coordinateKey{rackName: rackName, nodeIndex: binding.Coordinate.NodeIndex}
	key, exists := c.cleanedByCoordinate[coordinate]
	if !exists || key.inventoryName != binding.Coordinate.Group.InventoryName ||
		key.inventoryUID != binding.Coordinate.Group.InventoryUID ||
		key.rackGroup != binding.Coordinate.Group.RackGroup ||
		key.rackIndex != binding.Coordinate.RackIndex ||
		key.nodeName != binding.Node.Name || key.nodeUID != binding.Node.UID {
		return sgpurelease.Cleanup{}, false
	}
	reason, ready := c.cleaned[key]
	if !ready {
		return sgpurelease.Cleanup{}, false
	}
	return sgpurelease.Cleanup{
		RackName: rackName, RackUID: key.rackUID, Binding: binding, Reason: reason,
	}, true
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
		if order := cmp.Compare(a.NodeIndex, b.NodeIndex); order != 0 {
			return order
		}
		return cmp.Compare(string(a.NodeUID), string(b.NodeUID))
	})
}

// MatchesBinding reports whether a Node already carries the exact projection
// derived from a durable rack binding.
//
//nolint:cyclop // The predicate intentionally checks every exact identity component.
func MatchesBinding(node *corev1.Node, rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackNode) bool {
	if node == nil || rack == nil || slot == nil || !slot.BoundTo(node.Name, node.UID) ||
		node.Labels[sgpumetadata.AssignedLabel] != "true" {
		return false
	}
	clique, hasClique := cliqueValue(rack)
	if hasClique && node.Labels[sgpumetadata.CliqueLabel] != clique {
		return false
	}
	if !hasClique {
		if _, exists := node.Labels[sgpumetadata.CliqueLabel]; exists {
			return false
		}
	}
	assignment, err := sgpuassignment.EncodeAssignment(rack, slot)
	if err != nil || node.Annotations[sgpumetadata.AssignmentAnnotation] != assignment {
		return false
	}
	projectionLabels := map[string]any{sgpumetadata.AssignedLabel: "true"}
	if hasClique {
		projectionLabels[sgpumetadata.CliqueLabel] = clique
	}
	return len(foreignOwnedFields(node, projectionManagedFields(projectionLabels))) == 0 &&
		projectionFieldsOwned(node, projectionLabels)
}

func projectionLabels(node *corev1.Node, rack *mokkav1alpha1.SGPURack) (map[string]any, []string) {
	labels := map[string]any{sgpumetadata.AssignedLabel: "true"}
	incompatible := make([]string, 0, 2)
	if value, exists := node.Labels[sgpumetadata.AssignedLabel]; exists && value != "true" {
		incompatible = append(incompatible, sgpumetadata.AssignedLabel)
	}
	if clique, hasClique := cliqueValue(rack); hasClique {
		labels[sgpumetadata.CliqueLabel] = clique
		if value, exists := node.Labels[sgpumetadata.CliqueLabel]; exists && value != clique {
			incompatible = append(incompatible, sgpumetadata.CliqueLabel)
		}
	} else if _, exists := node.Labels[sgpumetadata.CliqueLabel]; exists {
		current, err := sgpuassignment.DecodeAssignment(node.Annotations[sgpumetadata.AssignmentAnnotation])
		slot := findSlotByUID(rack, node.UID)
		if err == nil && slot != nil && assignmentMatches(current, rack, slot) {
			labels[sgpumetadata.CliqueLabel] = nil
		} else {
			incompatible = append(incompatible, sgpumetadata.CliqueLabel)
		}
	}
	return labels, incompatible
}

func projectionIsCurrent(node *corev1.Node, labels map[string]any, assignment string) bool {
	if node.Annotations[sgpumetadata.AssignmentAnnotation] != assignment {
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
	fields = append(fields, managedMetadataField{section: "annotations", key: sgpumetadata.AssignmentAnnotation})
	for key := range labels {
		fields = append(fields, managedMetadataField{section: "labels", key: key})
	}
	return fields
}

func projectionPresentFields(labels map[string]any) []managedMetadataField {
	fields := make([]managedMetadataField, 0, 1+len(labels))
	fields = append(fields, managedMetadataField{section: "annotations", key: sgpumetadata.AssignmentAnnotation})
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
	if node.Annotations[sgpumetadata.AssignmentAnnotation] != assignment {
		conflicts = append(conflicts, sgpumetadata.AssignmentAnnotation)
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
		{section: "annotations", key: sgpumetadata.AssignmentAnnotation},
		{section: "labels", key: sgpumetadata.AssignedLabel},
		{section: "labels", key: sgpumetadata.CliqueLabel},
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
	if node.Annotations[sgpumetadata.AssignmentAnnotation] == assignment {
		retained = append(retained, sgpumetadata.AssignmentAnnotation)
	}
	if _, exists := node.Labels[sgpumetadata.AssignedLabel]; exists {
		retained = append(retained, sgpumetadata.AssignedLabel)
	}
	if _, exists := node.Labels[sgpumetadata.CliqueLabel]; exists {
		retained = append(retained, sgpumetadata.CliqueLabel)
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
	if rack == nil {
		return "", false
	}
	return fmt.Sprintf("%s.%d", rack.Spec.Identity.FabricUUID, rack.Spec.Identity.CliqueID), true
}

func assignmentMatches(current sgpuassignment.Assignment, rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackNode) bool {
	return current.Version == sgpuassignment.AssignmentVersion && rack != nil && slot != nil && slot.NodeRef != nil &&
		current.Inventory == (sgpuassignment.ObjectReference{Name: rack.Spec.InventoryRef.Name, UID: rack.Spec.InventoryRef.UID}) &&
		current.Rack == (sgpuassignment.ObjectReference{Name: rack.Name, UID: rack.UID}) &&
		current.RackGroup == rack.Spec.Identity.RackGroup && current.RackIndex == rack.Spec.Identity.RackIndex &&
		current.NodeIndex == slot.Index && current.NodeUID == slot.NodeRef.UID
}

func cleanupAssignmentMatches(current sgpuassignment.Assignment, needed sgpurelease.Cleanup) bool {
	binding := needed.Binding
	if current.Version != sgpuassignment.AssignmentVersion || current.Inventory.Name != binding.Coordinate.Group.InventoryName ||
		current.Inventory.UID != binding.Coordinate.Group.InventoryUID || current.Rack.Name != needed.RackName ||
		current.Rack.UID != needed.RackUID ||
		current.RackGroup != binding.Coordinate.Group.RackGroup || current.RackIndex != binding.Coordinate.RackIndex ||
		current.NodeIndex != binding.Coordinate.NodeIndex || current.NodeUID != binding.Node.UID {
		return false
	}
	return true
}

func (c *Controller) duplicateBindings(uid types.UID) (int, error) {
	racks, err := c.cache.RacksByNodeUID(uid)
	if err != nil {
		return 0, fmt.Errorf("get racks for Node UID %q: %w", uid, err)
	}
	count := 0
	for _, rack := range racks {
		for _, slot := range rack.Spec.Nodes {
			if slot.NodeRef != nil && slot.NodeRef.UID == uid {
				count++
			}
		}
	}
	return count, nil
}

func findSlotByUID(rack *mokkav1alpha1.SGPURack, uid types.UID) *mokkav1alpha1.SGPURackNode {
	for i := range rack.Spec.Nodes {
		if rack.Spec.Nodes[i].NodeRef != nil && rack.Spec.Nodes[i].NodeRef.UID == uid {
			return &rack.Spec.Nodes[i]
		}
	}
	return nil
}

func outcomeFor(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackNode) Outcome {
	return Outcome{
		InventoryName: rack.Spec.InventoryRef.Name, InventoryUID: rack.Spec.InventoryRef.UID,
		RackGroup: rack.Spec.Identity.RackGroup, RackName: rack.Name, RackUID: rack.UID,
		RackIndex: rack.Spec.Identity.RackIndex, NodeIndex: slot.Index,
		NodeName: slot.NodeRef.Name, NodeUID: slot.NodeRef.UID,
	}
}

func cleanupOutcome(needed sgpurelease.Cleanup) Outcome {
	binding := needed.Binding
	return Outcome{
		InventoryName: binding.Coordinate.Group.InventoryName, InventoryUID: binding.Coordinate.Group.InventoryUID,
		RackGroup: binding.Coordinate.Group.RackGroup, RackName: needed.RackName, RackUID: needed.RackUID,
		RackIndex: binding.Coordinate.RackIndex, NodeIndex: binding.Coordinate.NodeIndex,
		NodeName: needed.Binding.Node.Name, NodeUID: needed.Binding.Node.UID,
	}
}

func (c *Controller) record(outcome Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := bindingKeyForOutcome(outcome)
	coordinate := coordinateKeyForBinding(key)
	if previous, exists := c.outcomeByCoordinate[coordinate]; exists {
		c.deleteOutcomeLocked(previous)
	}
	if outcome.State == StateConflict && outcome.Reason == ReasonNodeMetadataConflict {
		c.putOutcomeLocked(outcome)
	}
}

func (c *Controller) fail(outcome Outcome, err error) (Outcome, error) {
	outcome.State, outcome.Reason, outcome.Message = StateError, ReasonProjectionError, err.Error()
	c.record(outcome)
	return outcome, err
}

func (c *Controller) conflict(outcome Outcome, nodeName string, fields []string) (Outcome, error) {
	outcome, conflict := conflictOutcome(outcome, nodeName, fields)
	c.record(outcome)
	// Apply conflicts are not retried, so this entry is the only log of one;
	// the controller leaves metadata another manager owns untouched.
	zap.L().Warn("Node metadata conflict blocks sGPU projection",
		append(outcomeFields(outcome), zap.Strings("fields", conflict.Fields))...)
	return outcome, conflict
}

// outcomeFields identifies a binding in log entries.
func outcomeFields(outcome Outcome) []zap.Field {
	return []zap.Field{
		zap.String("node", outcome.NodeName),
		zap.String("rack", outcome.RackName),
		zap.Int32("nodeIndex", outcome.NodeIndex),
		zap.String("inventory", outcome.InventoryName),
	}
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
	needed sgpurelease.Cleanup,
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
		c.cleaned[key] = needed.Reason
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

func (c *Controller) beginProjection(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	coordinate := coordinateKey{rackName: rack.Name, nodeIndex: slot.Index}
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

func (c *Controller) blockedCleanup(needed sgpurelease.Cleanup, node *corev1.Node) ([]string, bool) {
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
		node.Annotations[sgpumetadata.AssignmentAnnotation] == block.assignment {
		return nil, false
	}
	return fields, true
}

func (c *Controller) blockCleanup(
	needed sgpurelease.Cleanup,
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
	for _, key := range []string{sgpumetadata.AssignedLabel, sgpumetadata.CliqueLabel} {
		if value, exists := node.Labels[key]; exists {
			labels[key] = value
		}
	}
	annotations := make(map[string]string, 1)
	if value, exists := node.Annotations[sgpumetadata.AssignmentAnnotation]; exists {
		annotations[sgpumetadata.AssignmentAnnotation] = value
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
	return coordinateKey{rackName: key.rackName, nodeIndex: key.nodeIndex}
}

func (c *Controller) cleanupReady(outcome Outcome) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ready := c.cleaned[bindingKeyForOutcome(outcome)]
	return ready
}

func (c *Controller) operationLock(rackName string, nodeIndex int32) *sync.Mutex {
	hash := uint32(2166136261)
	for i := range len(rackName) {
		hash ^= uint32(rackName[i])
		hash *= 16777619
	}
	hash ^= uint32(nodeIndex)
	hash *= 16777619
	return &c.operations[hash%operationShards]
}

func bindingKeyForOutcome(outcome Outcome) bindingKey {
	return bindingKey{
		inventoryName: outcome.InventoryName, inventoryUID: outcome.InventoryUID,
		rackGroup: outcome.RackGroup, rackName: outcome.RackName, rackUID: outcome.RackUID,
		rackIndex: outcome.RackIndex, nodeIndex: outcome.NodeIndex,
		nodeName: outcome.NodeName, nodeUID: outcome.NodeUID,
	}
}

func bindingKeyForCleanup(needed sgpurelease.Cleanup) bindingKey {
	binding := needed.Binding
	return bindingKey{
		inventoryName: binding.Coordinate.Group.InventoryName, inventoryUID: binding.Coordinate.Group.InventoryUID,
		rackGroup: binding.Coordinate.Group.RackGroup, rackName: needed.RackName, rackUID: needed.RackUID,
		rackIndex: binding.Coordinate.RackIndex, nodeIndex: binding.Coordinate.NodeIndex,
		nodeName: binding.Node.Name, nodeUID: binding.Node.UID,
	}
}
