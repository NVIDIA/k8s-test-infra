// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/metadata"
)

const (
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

const (
	StateProjected State = "Projected"
	StateCleaned   State = "Cleaned"
	StateAbsent    State = "Absent"
	StateConflict  State = "Conflict"
	StateError     State = "Error"
)

const (
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

// MetadataConflictError reports a pre-existing value that the controller will
// not replace.
type MetadataConflictError struct {
	NodeName string
	Fields   []string
}

func (e *MetadataConflictError) Error() string {
	return fmt.Sprintf("Node %q has incompatible controller metadata fields %v", e.NodeName, e.Fields)
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

	mu       sync.RWMutex
	outcomes map[bindingKey]Outcome
	cleaned  map[bindingKey]struct{}
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

func NewController(cache Cache, patcher NodePatcher) *Controller {
	return &Controller{
		cache: cache, patcher: patcher,
		outcomes: make(map[bindingKey]Outcome), cleaned: make(map[bindingKey]struct{}),
	}
}

// Project reconciles metadata for one exact cached rack slot.
func (c *Controller) Project(ctx context.Context, rackName string, slotIndex int32) (Outcome, error) {
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
	if c.cleanupReady(outcome) {
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
	if len(incompatible) > 0 {
		slices.Sort(incompatible)
		conflict := &MetadataConflictError{NodeName: node.Name, Fields: incompatible}
		outcome.State, outcome.Reason, outcome.Message = StateConflict, ReasonNodeMetadataConflict, conflict.Error()
		c.record(outcome)
		return outcome, conflict
	}

	payload, err := nodeApplyPayload(node.Name, labels, map[string]any{AssignmentAnnotation: assignment})
	if err != nil {
		return c.fail(outcome, err)
	}
	_, err = c.patcher.Patch(ctx, node.Name, types.ApplyPatchType, payload, applyOptions())
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
	outcome.State, outcome.Reason = StateProjected, ReasonProjected
	c.record(outcome)
	return outcome, nil
}

// Cleanup removes only compatible controller keys while the assignment still
// identifies the exact binding being retired.
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
	payload, err := nodeApplyPayload(node.Name, nil, annotations)
	if err != nil {
		return c.failCleanup(outcome, err, exactBindingPresent)
	}
	_, err = c.patcher.Patch(ctx, node.Name, types.ApplyPatchType, payload, applyOptions())
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
	if len(incompatible) > 0 {
		slices.Sort(incompatible)
		conflict := &MetadataConflictError{NodeName: node.Name, Fields: incompatible}
		outcome.State, outcome.Reason, outcome.Message = StateConflict, ReasonNodeMetadataConflict, conflict.Error()
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
	slices.SortFunc(outcomes, func(a, b Outcome) int {
		if order := cmp.Compare(a.RackName, b.RackName); order != 0 {
			return order
		}
		if order := cmp.Compare(a.SlotIndex, b.SlotIndex); order != 0 {
			return order
		}
		return cmp.Compare(string(a.NodeUID), string(b.NodeUID))
	})
	return outcomes
}

// EncodeAssignment serializes the compact annotation without whitespace.
func EncodeAssignment(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) (string, error) {
	if rack == nil || rack.Name == "" || rack.UID == "" || slot == nil || slot.NodeRef == nil || slot.NodeRef.UID == "" {
		return "", fmt.Errorf("assignment requires exact rack, slot, and Node identities")
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
	return err == nil && assignmentMatches(assignment, rack, slot)
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

func nodeApplyPayload(name string, labels, annotations map[string]any) ([]byte, error) {
	metadata := map[string]any{"name": name}
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
	c.outcomes[bindingKeyForOutcome(outcome)] = outcome
}

func (c *Controller) fail(outcome Outcome, err error) (Outcome, error) {
	outcome.State, outcome.Reason, outcome.Message = StateError, ReasonProjectionError, err.Error()
	c.record(outcome)
	return outcome, err
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
	delete(c.outcomes, key)
	if exactRackPresent {
		c.cleaned[key] = struct{}{}
	} else {
		delete(c.cleaned, key)
	}
	c.mu.Unlock()
	return outcome
}

func (c *Controller) beginProjection(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackSlot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.outcomes {
		if key.rackName == rack.Name && key.slotIndex == slot.Index {
			delete(c.outcomes, key)
		}
	}
	for key := range c.cleaned {
		if key.rackName == rack.Name && key.slotIndex == slot.Index {
			delete(c.cleaned, key)
		}
	}
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
