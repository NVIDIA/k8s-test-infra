// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package allocate

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/NVIDIA/k8s-test-infra/internal/mokka/metadata"
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

		selector, err := CompilePlacementSelector(group.Selector)
		if err != nil {
			return CompiledGroups{}, fmt.Errorf("compile selector for %s: %w", group.Key, err)
		}
		compiled = append(compiled, compiledGroup{group: group, selector: selector})
	}
	slices.SortFunc(compiled, func(a, b compiledGroup) int {
		return compareGroupKey(a.group.Key, b.group.Key)
	})
	return CompiledGroups{groups: compiled}, nil
}

// ValidatePlacementSelector rejects selectors whose membership can be changed
// by projecting the binding that placement creates.
func ValidatePlacementSelector(selector *metav1.LabelSelector) error {
	_, err := CompilePlacementSelector(selector)
	return err
}

// CompilePlacementSelector validates and compiles a placement selector.
func CompilePlacementSelector(selector *metav1.LabelSelector) (labels.Selector, error) {
	if selector == nil {
		return labels.Everything(), nil
	}
	for _, key := range metadata.ProjectionLabelKeys() {
		if _, exists := selector.MatchLabels[key]; exists {
			return nil, fmt.Errorf("selector must not reference controller-owned label %q", key)
		}
		for _, expression := range selector.MatchExpressions {
			if expression.Key == key {
				return nil, fmt.Errorf("selector must not reference controller-owned label %q", key)
			}
		}
	}
	compiled, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, err
	}
	return compiled, nil
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
		return errors.New("group identity must include inventory name, UID, and rack group")
	}
	if group.Racks < 0 || group.NodesPerRack < 0 {
		return fmt.Errorf("group %s capacity must not be negative", group.Key)
	}
	return nil
}

func eligible(node Node) bool {
	return !node.Terminating && node.Labels[EligibleNodeLabel] == "true"
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
