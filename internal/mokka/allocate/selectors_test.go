// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package allocate

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/NVIDIA/k8s-test-infra/internal/mokka/metadata"
)

func TestClassifyEligibleNodes(t *testing.T) {
	groups, err := CompileGroups([]Group{
		{
			Key:      groupKey("inventory-a", "red"),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "red"}},
		},
		{
			Key: groupKey("inventory-b", "fast"),
			Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key: "speed", Operator: metav1.LabelSelectorOpIn, Values: []string{"fast"},
			}}},
		},
		{Key: groupKey("inventory-c", "all")},
	})
	require.NoError(t, err)

	classified, stats := Classify([]Node{
		node("red-fast", "1", 1, map[string]string{EligibleNodeLabel: "true", "pool": "red", "speed": "fast"}),
		node("red-slow", "2", 2, map[string]string{EligibleNodeLabel: "true", "pool": "red", "speed": "slow"}),
		node("unlabelled", "3", 3, map[string]string{"pool": "red"}),
		node("wrong-value", "4", 4, map[string]string{EligibleNodeLabel: "TRUE", "pool": "red"}),
	}, groups)

	require.Equal(t, []Classification{
		{
			Node: node("red-fast", "1", 1, map[string]string{EligibleNodeLabel: "true", "pool": "red", "speed": "fast"}),
			Candidates: []GroupKey{
				groupKey("inventory-a", "red"),
				groupKey("inventory-b", "fast"),
				groupKey("inventory-c", "all"),
			},
		},
		{
			Node: node("red-slow", "2", 2, map[string]string{EligibleNodeLabel: "true", "pool": "red", "speed": "slow"}),
			Candidates: []GroupKey{
				groupKey("inventory-a", "red"),
				groupKey("inventory-c", "all"),
			},
		},
	}, classified)
	require.Equal(t, int64(6), stats.SelectorEvaluations)
}

func TestCompileGroupsRejectsInvalidAndDuplicateSelectors(t *testing.T) {
	_, err := CompileGroups([]Group{
		{
			Key: groupKey("inventory-a", "invalid"),
			Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key:      "pool",
				Operator: metav1.LabelSelectorOpIn,
			}}},
		},
	})
	require.ErrorContains(t, err, "inventory-a/invalid")

	_, err = CompileGroups([]Group{
		{Key: groupKey("inventory-a", "same")},
		{Key: groupKey("inventory-a", "same")},
	})
	require.ErrorContains(t, err, "duplicate group")
}

func TestValidatePlacementSelectorRejectsProjectedLabels(t *testing.T) {
	expressions := []metav1.LabelSelectorRequirement{
		{Operator: metav1.LabelSelectorOpIn, Values: []string{"value"}},
		{Operator: metav1.LabelSelectorOpNotIn, Values: []string{"value"}},
		{Operator: metav1.LabelSelectorOpExists},
		{Operator: metav1.LabelSelectorOpDoesNotExist},
	}
	for _, key := range []string{metadata.AssignedLabel, metadata.CliqueLabel} {
		t.Run(key+"/matchLabels", func(t *testing.T) {
			err := ValidatePlacementSelector(&metav1.LabelSelector{MatchLabels: map[string]string{key: "value"}})
			require.EqualError(t, err, `selector must not reference controller-owned label "`+key+`"`)
		})
		for _, expression := range expressions {
			expression.Key = key
			t.Run(key+"/"+string(expression.Operator), func(t *testing.T) {
				err := ValidatePlacementSelector(&metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{expression}})
				require.EqualError(t, err, `selector must not reference controller-owned label "`+key+`"`)
			})
		}
	}
}

func TestValidatePlacementSelectorAcceptsOperatorOwnedLabels(t *testing.T) {
	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"pool": "gpu"},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "speed", Operator: metav1.LabelSelectorOpIn, Values: []string{"fast"}},
			{Key: "zone", Operator: metav1.LabelSelectorOpExists},
		},
	}
	require.NoError(t, ValidatePlacementSelector(selector))
	require.NoError(t, ValidatePlacementSelector(nil))
}

func TestEmptySelectorMatchesEveryEligibleNode(t *testing.T) {
	groups, err := CompileGroups([]Group{{
		Key:      groupKey("inventory-a", "all"),
		Selector: &metav1.LabelSelector{},
	}})
	require.NoError(t, err)

	classified, _ := Classify([]Node{
		node("eligible", "eligible-uid", 1, eligibleLabels()),
		node("not-eligible", "not-eligible-uid", 2, nil),
	}, groups)
	require.Equal(t, []Classification{{
		Node:       node("eligible", "eligible-uid", 1, eligibleLabels()),
		Candidates: []GroupKey{groupKey("inventory-a", "all")},
	}}, classified)
}

func groupKey(inventory, rackGroup string) GroupKey {
	return GroupKey{InventoryName: inventory, InventoryUID: types.UID(inventory + "-uid"), RackGroup: rackGroup}
}
