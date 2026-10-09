// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSupportedClocks_DecodeTheH100Table pins the decode against the one
// table whose shape is easy to state: the H100 capture lists two memory clocks
// with the same 110 graphics clocks, 1980 MHz down to 345 MHz in 15 MHz steps.
func TestSupportedClocks_DecodeTheH100Table(t *testing.T) {
	t.Parallel()
	p, err := Load(profilesDir, "h100")
	require.NoError(t, err)

	table := p.SupportedClocks()
	require.Len(t, table, 2)
	require.Equal(t, []int{2619, 1593}, []int{table[0].MemoryMHz, table[1].MemoryMHz})
	for _, mc := range table {
		require.Len(t, mc.GraphicsMHz, 110, "memory clock %d", mc.MemoryMHz)
		require.Equal(t, 1980, mc.GraphicsMHz[0])
		require.Equal(t, 345, mc.GraphicsMHz[len(mc.GraphicsMHz)-1])
	}

	table[0].GraphicsMHz[0] = 1
	require.Equal(t, 1980, p.SupportedClocks()[0].GraphicsMHz[0], "the accessor must hand out a copy")
}

// TestAlternateApplicationsClocks_IsASupportedNonDefaultPair is what the
// runtime `nvidia-smi -ac` spec relies on for every profile: the pair it sets
// must be one the mock accepts and one the defaults cannot be mistaken for.
func TestAlternateApplicationsClocks_IsASupportedNonDefaultPair(t *testing.T) {
	t.Parallel()
	for _, name := range KnownProfiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := Load(profilesDir, name)
			require.NoError(t, err)

			defMem, defGfx := p.DefaultApplicationsClocksMHz()
			require.True(t, supportsPair(p, defMem, defGfx),
				"default applications clocks (%d, %d) must be a supported pair, or -rac restores what -ac refuses", defMem, defGfx)

			mem, gfx, ok := p.AlternateApplicationsClocksMHz()
			require.True(t, ok)
			require.True(t, supportsPair(p, mem, gfx), "(%d, %d) not in the supported table", mem, gfx)
			require.NotEqual(t, [2]int{defMem, defGfx}, [2]int{mem, gfx})
		})
	}
}

func TestAlternateApplicationsClocks_NoneWithoutATable(t *testing.T) {
	t.Parallel()
	_, _, ok := Profile{}.AlternateApplicationsClocksMHz()
	require.False(t, ok)

	onlyDefault := Profile{
		appDefaultMemMHz: 5001, appDefaultGfxMHz: 1590,
		supportedClocks: []SupportedMemoryClock{{MemoryMHz: 5001, GraphicsMHz: []int{1590}}},
	}
	_, _, ok = onlyDefault.AlternateApplicationsClocksMHz()
	require.False(t, ok, "a table holding only the default pair has nothing to switch to")
}

func supportsPair(p Profile, mem, gfx int) bool {
	for _, mc := range p.SupportedClocks() {
		if mc.MemoryMHz == mem && slices.Contains(mc.GraphicsMHz, gfx) {
			return true
		}
	}
	return false
}
