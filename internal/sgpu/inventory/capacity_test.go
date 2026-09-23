// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package inventory

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func TestAdmitInventoriesAdmitsWholeInventoriesThenLetsGrowthProceedInOrder(t *testing.T) {
	t.Parallel()

	nodes := func(count int64) DeclaredCapacity { return DeclaredCapacity{Racks: count, Nodes: count} }
	candidate := func(name string, actual, target DeclaredCapacity) admissionInventory {
		return admissionInventory{
			instance:       inventoryInstance{name: name, uid: types.UID(name + "-uid")},
			targetCapacity: target,
			growth:         positiveCapacityDifference(target, actual),
		}
	}
	candidates := []admissionInventory{
		candidate("shrinking", nodes(60_000), nodes(20_000)),
		candidate("growing", nodes(0), nodes(70_000)),
		candidate("too-large", nodes(0), nodes(11_000)),
		candidate("small", nodes(0), nodes(5_000)),
		candidate("steady", nodes(5_000), nodes(5_000)),
	}

	admitted := admitInventories(candidates, DeclaredCapacity{}, nodes(65_000))

	type outcome struct {
		name  string
		ready bool
	}
	got := make([]outcome, 0, len(admitted))
	for _, inventory := range admitted {
		got = append(got, outcome{name: inventory.instance.name, ready: inventory.ready})
	}
	// too-large would exceed the limit beside earlier admissions, so it is
	// skipped whole. growing cannot fit the live topology until shrinking
	// releases its racks, and small waits behind it to keep admission order.
	require.Equal(t, []outcome{
		{name: "shrinking", ready: true},
		{name: "growing"},
		{name: "small"},
		{name: "steady", ready: true},
	}, got)
}

func TestAdmitInventoriesAdmitsUpToNodeLimit(t *testing.T) {
	const declarationCount = MaxInventoryNodes + 1
	candidates := make([]admissionInventory, declarationCount)
	for index := range candidates {
		candidates[index] = admissionInventory{
			instance:       inventoryInstance{name: fmt.Sprintf("inventory-%06d", index)},
			targetCapacity: DeclaredCapacity{Racks: 1, Nodes: 1, GPUs: 1},
		}
	}

	admitted := admitInventories(candidates, DeclaredCapacity{}, DeclaredCapacity{})

	require.Len(t, admitted, int(MaxInventoryNodes))
	require.Equal(t, "inventory-099999", admitted[len(admitted)-1].instance.name)
}
