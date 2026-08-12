// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

// Package allocate plans stable Node-to-slot bindings from immutable snapshots.
// It performs no Kubernetes API operations and does not mutate its inputs.
package allocate

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

// GroupKey identifies one rack group in an exact inventory instance.
type GroupKey struct {
	InventoryName string
	InventoryUID  types.UID
	RackGroup     string
}

func (key GroupKey) String() string {
	return key.InventoryName + "/" + key.RackGroup
}

// Group is a resolved placement and capacity declaration.
type Group struct {
	Key          GroupKey
	Selector     *metav1.LabelSelector
	Racks        int32
	SlotsPerRack int32
}

// Node is the allocation-relevant portion of a Kubernetes Node.
type Node struct {
	Name              string
	UID               types.UID
	CreationTimestamp time.Time
	Labels            map[string]string
}

// NodeReference pins a binding to an exact Node instance.
type NodeReference struct {
	Name string
	UID  types.UID
}

// Coordinate identifies one durable rack slot.
type Coordinate struct {
	Group     GroupKey
	RackIndex int32
	SlotIndex int32
}

// Binding assigns an exact Node instance to one coordinate.
type Binding struct {
	Coordinate Coordinate
	Node       NodeReference
}

// ReleaseReason explains why an existing binding is no longer valid.
type ReleaseReason string

const (
	ReleaseGroupRemoved     ReleaseReason = "GroupRemoved"
	ReleaseCapacityShrink   ReleaseReason = "CapacityShrink"
	ReleaseNodeGone         ReleaseReason = "NodeGone"
	ReleaseNodeIneligible   ReleaseReason = "NodeIneligible"
	ReleaseSelectorMismatch ReleaseReason = "SelectorMismatch"
)

// Release is cleanup work that must finish before its slot can be reused.
type Release struct {
	Binding Binding
	Reason  ReleaseReason
}

// ConflictKind identifies placement data that cannot be resolved arbitrarily.
type ConflictKind string

const (
	ConflictSelectorOverlap  ConflictKind = "SelectorOverlap"
	ConflictDuplicateBinding ConflictKind = "DuplicateBinding"
)

// Conflict contains either overlapping candidates or duplicate bindings.
type Conflict struct {
	Kind       ConflictKind
	Node       Node
	Candidates []GroupKey
	Bindings   []Binding
}

// Stats exposes bounded-work counters for large-snapshot tests and benchmarks.
type Stats struct {
	SelectorEvaluations int64
	BindingLookups      int64
	SlotVisits          int64
}

// Input is a complete immutable allocation snapshot.
type Input struct {
	Groups   []Group
	Nodes    []Node
	Bindings []Binding
}

// Plan separates durable retention, cleanup, new writes, and unresolved Nodes.
type Plan struct {
	Retained  []Binding
	Released  []Release
	Assigned  []Binding
	Bindings  []Binding
	Pending   []Node
	Conflicts []Conflict
	Stats     Stats
}

type groupState struct {
	group    Group
	selector labels.Selector
	occupied map[uint64]struct{}
	cursor   int64
}

// Allocate produces a deterministic plan without compacting valid bindings or
// reusing coordinates whose cleanup is still pending.
func Allocate(input Input) (Plan, error) {
	compiled, err := CompileGroups(input.Groups)
	if err != nil {
		return Plan{}, err
	}

	nodesByUID, err := indexNodes(input.Nodes)
	if err != nil {
		return Plan{}, err
	}
	if err := validateBindingCoordinates(input.Bindings); err != nil {
		return Plan{}, err
	}

	states := make(map[GroupKey]*groupState, len(compiled.groups))
	for _, group := range compiled.groups {
		states[group.group.Key] = &groupState{
			group:    group.group,
			selector: group.selector,
			occupied: make(map[uint64]struct{}),
		}
	}
	for _, binding := range input.Bindings {
		if state := states[binding.Coordinate.Group]; state != nil &&
			coordinateInCapacity(binding.Coordinate, state.group) {
			state.occupied[slotKey(binding.Coordinate)] = struct{}{}
		}
	}

	plan := Plan{}
	bindingsByUID := make(map[types.UID][]Binding, len(input.Bindings))
	for _, binding := range input.Bindings {
		plan.Stats.BindingLookups++
		bindingsByUID[binding.Node.UID] = append(bindingsByUID[binding.Node.UID], binding)

		state := states[binding.Coordinate.Group]
		if state == nil {
			plan.Released = append(plan.Released, Release{Binding: binding, Reason: ReleaseGroupRemoved})
			continue
		}
		if !coordinateInCapacity(binding.Coordinate, state.group) {
			plan.Released = append(plan.Released, Release{Binding: binding, Reason: ReleaseCapacityShrink})
			continue
		}
		node, exists := nodesByUID[binding.Node.UID]
		if !exists {
			plan.Released = append(plan.Released, Release{Binding: binding, Reason: ReleaseNodeGone})
			continue
		}
		if !eligible(node) {
			plan.Released = append(plan.Released, Release{Binding: binding, Reason: ReleaseNodeIneligible})
			continue
		}
		plan.Stats.SelectorEvaluations++
		if !state.selector.Matches(labels.Set(node.Labels)) {
			plan.Released = append(plan.Released, Release{Binding: binding, Reason: ReleaseSelectorMismatch})
			continue
		}
		plan.Retained = append(plan.Retained, binding)
	}
	sortBindings(plan.Retained)
	sortReleases(plan.Released)

	boundUIDs := make(map[types.UID]struct{}, len(bindingsByUID))
	retainedUIDs := make(map[types.UID]struct{}, len(plan.Retained))
	for _, binding := range plan.Retained {
		retainedUIDs[binding.Node.UID] = struct{}{}
	}
	for uid, bindings := range bindingsByUID {
		boundUIDs[uid] = struct{}{}
		if len(bindings) < 2 {
			continue
		}
		node, exists := nodesByUID[uid]
		if !exists {
			node = Node{Name: bindings[0].Node.Name, UID: uid}
		}
		conflictBindings := slices.Clone(bindings)
		sortBindings(conflictBindings)
		plan.Conflicts = append(plan.Conflicts, Conflict{
			Kind:     ConflictDuplicateBinding,
			Node:     node,
			Bindings: conflictBindings,
		})
	}

	classified, classificationStats := Classify(input.Nodes, compiled)
	plan.Stats.SelectorEvaluations += classificationStats.SelectorEvaluations
	slices.SortFunc(classified, func(a, b Classification) int {
		return compareNodes(a.Node, b.Node)
	})
	for _, classification := range classified {
		if _, bound := boundUIDs[classification.Node.UID]; bound {
			if len(bindingsByUID[classification.Node.UID]) > 1 {
				continue
			}
			if _, retained := retainedUIDs[classification.Node.UID]; retained {
				continue
			}
			switch len(classification.Candidates) {
			case 0:
			case 1:
				plan.Pending = append(plan.Pending, classification.Node)
			default:
				plan.Conflicts = append(plan.Conflicts, Conflict{
					Kind:       ConflictSelectorOverlap,
					Node:       classification.Node,
					Candidates: slices.Clone(classification.Candidates),
				})
			}
			continue
		}

		switch len(classification.Candidates) {
		case 0:
			continue
		case 1:
			state := states[classification.Candidates[0]]
			coordinate, ok := nextFreeCoordinate(state, &plan.Stats)
			if !ok {
				plan.Pending = append(plan.Pending, classification.Node)
				continue
			}
			assigned := Binding{
				Coordinate: coordinate,
				Node: NodeReference{
					Name: classification.Node.Name,
					UID:  classification.Node.UID,
				},
			}
			plan.Assigned = append(plan.Assigned, assigned)
		default:
			plan.Conflicts = append(plan.Conflicts, Conflict{
				Kind:       ConflictSelectorOverlap,
				Node:       classification.Node,
				Candidates: slices.Clone(classification.Candidates),
			})
		}
	}

	sortBindings(plan.Assigned)
	plan.Bindings = make([]Binding, 0, len(plan.Retained)+len(plan.Assigned))
	plan.Bindings = append(plan.Bindings, plan.Retained...)
	plan.Bindings = append(plan.Bindings, plan.Assigned...)
	sortConflicts(plan.Conflicts)
	return plan, nil
}

func indexNodes(nodes []Node) (map[types.UID]Node, error) {
	indexed := make(map[types.UID]Node, len(nodes))
	for _, node := range nodes {
		if node.Name == "" || node.UID == "" {
			return nil, fmt.Errorf("Node identity must include name and UID")
		}
		if _, exists := indexed[node.UID]; exists {
			return nil, fmt.Errorf("duplicate Node UID %q", node.UID)
		}
		indexed[node.UID] = node
	}
	return indexed, nil
}

func validateBindingCoordinates(bindings []Binding) error {
	coordinates := make(map[Coordinate]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.Node.Name == "" || binding.Node.UID == "" {
			return fmt.Errorf("binding Node identity must include name and UID")
		}
		if binding.Coordinate.RackIndex < 0 || binding.Coordinate.SlotIndex < 0 {
			return fmt.Errorf("binding coordinate must not be negative")
		}
		if _, exists := coordinates[binding.Coordinate]; exists {
			return fmt.Errorf("duplicate slot binding at %+v", binding.Coordinate)
		}
		coordinates[binding.Coordinate] = struct{}{}
	}
	return nil
}

func nextFreeCoordinate(state *groupState, stats *Stats) (Coordinate, bool) {
	total := int64(state.group.Racks) * int64(state.group.SlotsPerRack)
	for state.cursor < total {
		offset := state.cursor
		state.cursor++
		stats.SlotVisits++
		coordinate := Coordinate{
			Group:     state.group.Key,
			RackIndex: int32(offset / int64(state.group.SlotsPerRack)),
			SlotIndex: int32(offset % int64(state.group.SlotsPerRack)),
		}
		key := slotKey(coordinate)
		if _, occupied := state.occupied[key]; occupied {
			continue
		}
		state.occupied[key] = struct{}{}
		return coordinate, true
	}
	return Coordinate{}, false
}

func coordinateInCapacity(coordinate Coordinate, group Group) bool {
	return coordinate.RackIndex >= 0 &&
		coordinate.RackIndex < group.Racks &&
		coordinate.SlotIndex >= 0 &&
		coordinate.SlotIndex < group.SlotsPerRack
}

func slotKey(coordinate Coordinate) uint64 {
	return uint64(uint32(coordinate.RackIndex))<<32 | uint64(uint32(coordinate.SlotIndex))
}

func compareNodes(a, b Node) int {
	if order := a.CreationTimestamp.Compare(b.CreationTimestamp); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Name, b.Name); order != 0 {
		return order
	}
	return cmp.Compare(string(a.UID), string(b.UID))
}

func compareBindings(a, b Binding) int {
	if order := compareGroupKey(a.Coordinate.Group, b.Coordinate.Group); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Coordinate.RackIndex, b.Coordinate.RackIndex); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Coordinate.SlotIndex, b.Coordinate.SlotIndex); order != 0 {
		return order
	}
	return cmp.Compare(string(a.Node.UID), string(b.Node.UID))
}

func sortBindings(bindings []Binding) {
	slices.SortFunc(bindings, compareBindings)
}

func sortReleases(releases []Release) {
	slices.SortFunc(releases, func(a, b Release) int {
		if order := compareBindings(a.Binding, b.Binding); order != 0 {
			return order
		}
		return cmp.Compare(a.Reason, b.Reason)
	})
}

func sortConflicts(conflicts []Conflict) {
	slices.SortFunc(conflicts, func(a, b Conflict) int {
		if order := compareNodes(a.Node, b.Node); order != 0 {
			return order
		}
		return cmp.Compare(a.Kind, b.Kind)
	})
}
