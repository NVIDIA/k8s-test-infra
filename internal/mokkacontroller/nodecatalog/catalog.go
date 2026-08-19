// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

// Package nodecatalog maintains the controller's rebuildable, allocation-
// oriented view of eligible Kubernetes Nodes.
package nodecatalog

import (
	"cmp"
	"slices"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"

	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/metadata"
)

// SpecFingerprintAnnotation exists only in the compact informer cache so Node
// spec changes can advance allocation state without retaining every Node spec.
const SpecFingerprintAnnotation = "mokka.nvidia.com/internal-node-spec-fingerprint"

// Record is one immutable Node observation shared by catalog indexes and
// reconciliation snapshots.
type Record struct {
	node       *corev1.Node
	allocation allocate.Node
}

// Node returns the informer-owned immutable Kubernetes view.
func (r *Record) Node() *corev1.Node { return r.node }

// Allocation returns the allocation-relevant view without copying labels.
func (r *Record) Allocation() allocate.Node { return r.allocation }

// Snapshot is one immutable catalog generation. Its slices are shared between
// reconciles and must be treated as read-only.
type Snapshot struct {
	records    []*Record
	allocation []allocate.Node
	generation uint64
}

// Records returns all records in deterministic Node identity order.
func (s *Snapshot) Records() []*Record { return s.records }

// AllocationNodes returns the cached allocator input for this generation.
func (s *Snapshot) AllocationNodes() []allocate.Node { return s.allocation }

// Generation identifies the exact allocation-relevant Node input revision.
func (s *Snapshot) Generation() uint64 { return s.generation }

type recordSet map[*Record]struct{}

type labelValue struct {
	key   string
	value string
}

// Catalog is local derived state populated from one eligible-Node informer.
// Kubernetes remains authoritative and a restart rebuilds the entire catalog.
type Catalog struct {
	mu sync.RWMutex

	byName       map[string]*Record
	byUID        map[types.UID]*Record
	byLabelKey   map[string]recordSet
	byLabelValue map[labelValue]recordSet
	snapshot     *Snapshot
	generation   uint64
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{
		byName:       make(map[string]*Record),
		byUID:        make(map[types.UID]*Record),
		byLabelKey:   make(map[string]recordSet),
		byLabelValue: make(map[labelValue]recordSet),
	}
}

// Upsert publishes a new immutable record for the observed Node generation.
func (c *Catalog) Upsert(node *corev1.Node) {
	if node == nil || node.Name == "" || node.UID == "" {
		return
	}
	allocation := allocate.Node{
		Name: node.Name, UID: node.UID,
		CreationTimestamp: node.CreationTimestamp.Time, Labels: node.Labels,
	}
	record := &Record{
		node:       node,
		allocation: allocation,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	allocationChanged := true
	if previous := c.byName[node.Name]; previous != nil && previous.node.UID == node.UID {
		allocationChanged = !allocationNodeEqual(previous, node, allocation)
	}
	if previous := c.byName[node.Name]; previous != nil {
		c.removeLocked(previous)
	}
	if previous := c.byUID[node.UID]; previous != nil {
		c.removeLocked(previous)
	}
	c.byName[node.Name] = record
	c.byUID[node.UID] = record
	c.indexLocked(record)
	if allocationChanged {
		c.generation++
	}
	c.snapshot = nil
}

// Delete removes only the exact Node instance, so a stale delete cannot evict
// a same-name replacement.
func (c *Catalog) Delete(name string, uid types.UID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.byName[name]
	if record == nil || record.node.UID != uid {
		return
	}
	c.removeLocked(record)
	c.generation++
	c.snapshot = nil
}

// Generation returns the current allocation-relevant Node revision.
func (c *Catalog) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// GetByName returns the current immutable record for a Node name.
func (c *Catalog) GetByName(name string) (*Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, found := c.byName[name]
	return record, found
}

// GetByUID returns the current immutable record for an exact Node UID.
func (c *Catalog) GetByUID(uid types.UID) *Record {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.byUID[uid]
}

// Snapshot returns a cached immutable view until the next Node change.
func (c *Catalog) Snapshot() *Snapshot {
	c.mu.RLock()
	current := c.snapshot
	c.mu.RUnlock()
	if current != nil {
		return current
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot != nil {
		return c.snapshot
	}
	records := make([]*Record, 0, len(c.byName))
	for _, record := range c.byName {
		records = append(records, record)
	}
	sortRecords(records)
	allocation := make([]allocate.Node, len(records))
	for index, record := range records {
		allocation[index] = record.allocation
	}
	c.snapshot = &Snapshot{records: records, allocation: allocation, generation: c.generation}
	return c.snapshot
}

// Candidates returns a positively indexed candidate set when the selector has
// one. Negative and match-all selectors correctly use the universe.
func (c *Catalog) Candidates(selector labels.Selector) []*Record {
	if labels.MatchesNothing(selector) {
		return nil
	}
	requirements, selectable := selector.Requirements()
	if !selectable {
		return nil
	}
	for _, requirement := range requirements {
		switch requirement.Operator() {
		case selection.Equals, selection.DoubleEquals, selection.In:
			c.mu.RLock()
			set := make(recordSet)
			for _, value := range requirement.Values().List() {
				for record := range c.byLabelValue[labelValue{key: requirement.Key(), value: value}] {
					set[record] = struct{}{}
				}
			}
			records := recordsFromSet(set)
			c.mu.RUnlock()
			return records
		default:
			continue
		}
	}
	for _, requirement := range requirements {
		if requirement.Operator() == selection.Exists {
			c.mu.RLock()
			records := recordsFromSet(c.byLabelKey[requirement.Key()])
			c.mu.RUnlock()
			return records
		}
	}
	return c.Snapshot().Records()
}

func (c *Catalog) indexLocked(record *Record) {
	for key, value := range record.node.Labels {
		addToSet(c.byLabelKey, key, record)
		addToSet(c.byLabelValue, labelValue{key: key, value: value}, record)
	}
}

func (c *Catalog) removeLocked(record *Record) {
	if c.byName[record.node.Name] == record {
		delete(c.byName, record.node.Name)
	}
	if c.byUID[record.node.UID] == record {
		delete(c.byUID, record.node.UID)
	}
	for key, value := range record.node.Labels {
		removeFromSet(c.byLabelKey, key, record)
		removeFromSet(c.byLabelValue, labelValue{key: key, value: value}, record)
	}
}

func allocationNodeEqual(previous *Record, node *corev1.Node, allocation allocate.Node) bool {
	return previous.allocation.Name == allocation.Name &&
		previous.allocation.UID == allocation.UID &&
		previous.allocation.CreationTimestamp.Equal(allocation.CreationTimestamp) &&
		allocationLabelsEqual(previous.allocation.Labels, allocation.Labels) &&
		nodeSpecEqual(previous.node, node) &&
		equality.Semantic.DeepEqual(previous.node.DeletionTimestamp, node.DeletionTimestamp)
}

func allocationLabelsEqual(previous, current map[string]string) bool {
	for key, value := range previous {
		if key == metadata.AssignedLabel || key == metadata.CliqueLabel {
			continue
		}
		if current[key] != value {
			return false
		}
	}
	for key := range current {
		if key == metadata.AssignedLabel || key == metadata.CliqueLabel {
			continue
		}
		if _, exists := previous[key]; !exists {
			return false
		}
	}
	return true
}

func nodeSpecEqual(previous, current *corev1.Node) bool {
	previousFingerprint := previous.Annotations[SpecFingerprintAnnotation]
	currentFingerprint := current.Annotations[SpecFingerprintAnnotation]
	if previousFingerprint != "" || currentFingerprint != "" {
		return previousFingerprint == currentFingerprint
	}
	return equality.Semantic.DeepEqual(previous.Spec, current.Spec)
}

func addToSet[K comparable](index map[K]recordSet, key K, record *Record) {
	set := index[key]
	if set == nil {
		set = make(recordSet)
		index[key] = set
	}
	set[record] = struct{}{}
}

func removeFromSet[K comparable](index map[K]recordSet, key K, record *Record) {
	set := index[key]
	delete(set, record)
	if len(set) == 0 {
		delete(index, key)
	}
}

func recordsFromSet(set recordSet) []*Record {
	records := make([]*Record, 0, len(set))
	for record := range set {
		records = append(records, record)
	}
	return records
}

func sortRecords(records []*Record) {
	slices.SortFunc(records, func(a, b *Record) int {
		if order := cmp.Compare(a.node.Name, b.node.Name); order != 0 {
			return order
		}
		return cmp.Compare(string(a.node.UID), string(b.node.UID))
	})
}
