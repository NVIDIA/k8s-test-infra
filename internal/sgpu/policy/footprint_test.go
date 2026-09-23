// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithinKeepsListedIndexesBelowTheBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sorted []int32
		bound  int32
		want   indexes
	}{
		{name: "unlisted axis selects every index", bound: 4, want: indexes{every: true}},
		{name: "listed indexes are clipped to the bound", sorted: []int32{-1, 0, 3, 5}, bound: 4, want: indexes{listed: []int32{0, 3}}},
		{name: "zero bound keeps nothing", sorted: []int32{0, 1}, want: indexes{listed: []int32{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := within(tt.sorted, tt.bound)
			require.Equal(t, tt.want.every, got.every)
			require.ElementsMatch(t, tt.want.listed, got.listed)
		})
	}
}

func TestSelectionsOverlapOnlyWhenEveryAxisIntersects(t *testing.T) {
	t.Parallel()

	every := indexes{every: true}
	listed := func(values ...int32) indexes { return indexes{listed: values} }
	tests := []struct {
		name string
		a, b selection
		want bool
	}{
		{
			name: "unlisted axes overlap anything",
			a:    selection{racks: every, nodes: every, gpus: every},
			b:    selection{racks: listed(1), nodes: listed(2), gpus: listed(3)},
			want: true,
		},
		{
			name: "shared indexes on every axis overlap",
			a:    selection{racks: listed(0, 1), nodes: every, gpus: listed(2)},
			b:    selection{racks: listed(1), nodes: listed(5), gpus: listed(0, 2)},
			want: true,
		},
		{
			name: "one disjoint axis keeps the selections apart",
			a:    selection{racks: listed(0, 1), nodes: every, gpus: listed(2)},
			b:    selection{racks: listed(1), nodes: every, gpus: listed(3)},
		},
		{
			name: "an empty axis overlaps nothing",
			a:    selection{racks: listed(), nodes: every, gpus: every},
			b:    selection{racks: every, nodes: every, gpus: every},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.a.overlaps(tt.b))
			require.Equal(t, tt.want, tt.b.overlaps(tt.a))
		})
	}
}

func TestFootprintsOverlapOnlyInASharedRackGroup(t *testing.T) {
	t.Parallel()

	all := selection{racks: indexes{every: true}, nodes: indexes{every: true}, gpus: indexes{every: true}}
	require.True(t, footprint{"training": all}.overlaps(footprint{"inference": all, "training": all}))
	require.False(t, footprint{"training": all}.overlaps(footprint{"inference": all}))
	require.False(t, footprint{}.overlaps(footprint{"training": all}))
}
