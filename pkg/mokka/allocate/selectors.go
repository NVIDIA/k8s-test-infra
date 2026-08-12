// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package allocate

import (
	"cmp"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// EligibleNodeLabel gates all Mokka placement.
const EligibleNodeLabel = "mokka.nvidia.com/sgpu-node"

type compiledGroup struct {
	group    Group
	selector labels.Selector
}

// CompiledGroups caches selector parsing for repeated classifications.
type CompiledGroups struct {
	groups []compiledGroup
}

// Classification is the ordered candidate set for one eligible Node.
type Classification struct {
	Node       Node
	Candidates []GroupKey
}

// CompileGroups validates group identity, capacity, and selectors once.
func CompileGroups(groups []Group) (CompiledGroups, error) {
	compiled := make([]compiledGroup, 0, len(groups))
	seen := make(map[GroupKey]struct{}, len(groups))
	for _, group := range groups {
		if err := validateGroup(group); err != nil {
			return CompiledGroups{}, err
		}
		if _, exists := seen[group.Key]; exists {
			return CompiledGroups{}, fmt.Errorf("duplicate group %s", group.Key)
		}
		seen[group.Key] = struct{}{}

		selector := labels.Everything()
		if group.Selector != nil {
			var err error
			selector, err = metav1.LabelSelectorAsSelector(group.Selector)
			if err != nil {
				return CompiledGroups{}, fmt.Errorf("compile selector for %s: %w", group.Key, err)
			}
		}
		compiled = append(compiled, compiledGroup{group: group, selector: selector})
	}
	slices.SortFunc(compiled, func(a, b compiledGroup) int {
		return compareGroupKey(a.group.Key, b.group.Key)
	})
	return CompiledGroups{groups: compiled}, nil
}

// Classify evaluates every eligible Node against precompiled group selectors.
func Classify(nodes []Node, groups CompiledGroups) ([]Classification, Stats) {
	classified := make([]Classification, 0, len(nodes))
	var stats Stats
	for _, node := range nodes {
		if !eligible(node) {
			continue
		}
		candidates := make([]GroupKey, 0, 1)
		nodeLabels := labels.Set(node.Labels)
		for _, group := range groups.groups {
			stats.SelectorEvaluations++
			if group.selector.Matches(nodeLabels) {
				candidates = append(candidates, group.group.Key)
			}
		}
		classified = append(classified, Classification{Node: node, Candidates: candidates})
	}
	return classified, stats
}

func validateGroup(group Group) error {
	if group.Key.InventoryName == "" || group.Key.InventoryUID == "" || group.Key.RackGroup == "" {
		return fmt.Errorf("group identity must include inventory name, UID, and rack group")
	}
	if group.Racks < 0 || group.SlotsPerRack < 0 {
		return fmt.Errorf("group %s capacity must not be negative", group.Key)
	}
	return nil
}

func eligible(node Node) bool {
	return node.Labels[EligibleNodeLabel] == "true"
}

func compareGroupKey(a, b GroupKey) int {
	if order := cmp.Compare(a.InventoryName, b.InventoryName); order != 0 {
		return order
	}
	if order := cmp.Compare(string(a.InventoryUID), string(b.InventoryUID)); order != 0 {
		return order
	}
	return cmp.Compare(a.RackGroup, b.RackGroup)
}
