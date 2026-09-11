// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const hostLine = "xfs 1556480 1 - Live 0x0000000000000000\n"

func TestProcModules_AppendsNVIDIAEntriesAfterHostLines(t *testing.T) {
	t.Parallel()

	out := ProcModules(hostLine, Modules("550.163.01"))

	require.Contains(t, out, hostLine)
	require.Contains(t, out, "nvidia_uvm 3411968 0 - Live 0x0000000000000000\n")
	require.Contains(t, out, "nvidia 62312448 1 nvidia_uvm, Live 0x0000000000000000\n")
}

func TestProcModules_KeepsHostEntryForAnAlreadyLoadedModule(t *testing.T) {
	t.Parallel()

	src := "nvidia 62312448 7 nvidia_uvm, Live 0x0000000000000000\n"

	out := ProcModules(src, Modules("550.163.01"))

	require.Equal(t, 1, strings.Count(out, "nvidia 62312448"))
	require.Contains(t, out, "7 nvidia_uvm,")
}

func TestProcModules_IdempotentAgainstItsOwnOutput(t *testing.T) {
	t.Parallel()

	mods := Modules("550.163.01")

	first := ProcModules(hostLine, mods)
	second := ProcModules(first, mods)

	require.Equal(t, first, second)
}

func TestProcModules_TerminatesASourceMissingItsFinalNewline(t *testing.T) {
	t.Parallel()

	out := ProcModules("xfs 1556480 1 - Live 0x0000000000000000", Modules("550.163.01"))

	require.Contains(t, out, "0x0000000000000000\nnvidia")
}

func TestProcModules_RendersEntriesForAnEmptySource(t *testing.T) {
	t.Parallel()

	out := ProcModules("", Modules("550.163.01"))

	require.Equal(t, 2, strings.Count(out, "\n"))
	require.True(t, strings.HasPrefix(out, "nvidia "))
}

func TestLoadedModules_ReadsSizeAndRefcntAndIgnoresBlankLines(t *testing.T) {
	t.Parallel()

	mods := loadedModules("xfs 1556480 1 - Live 0x0\n\nnvidia_uvm 3411968 0 - Live 0x0\n")

	require.Len(t, mods, 2)
	require.Equal(t, hostModule{coreSize: "1556480", refcnt: "1", holdersKnown: true}, mods["xfs"])
	require.Equal(t, hostModule{coreSize: "3411968", refcnt: "0", holdersKnown: true}, mods["nvidia_uvm"])
	require.NotContains(t, mods, "")
}

func TestLoadedModules_SeparatesAnAbsentDependencyFieldFromNone(t *testing.T) {
	t.Parallel()

	mods := loadedModules("xfs 1556480 1 - Live 0x0\nbtrfs 1000 0\n")

	require.True(t, mods["xfs"].holdersKnown, "a dash means the host reported no holders")
	require.Empty(t, mods["xfs"].holders)
	require.False(t, mods["btrfs"].holdersKnown, "a truncated line reports nothing about holders")
}

func TestModules_GiveNVIDIAAVersionAndItsHolderNone(t *testing.T) {
	t.Parallel()

	mods := Modules("550.163.01")

	require.Equal(t, NVIDIA, mods[0].Name)
	require.Equal(t, "550.163.01", mods[0].Version)
	require.Equal(t, 1, mods[0].Refcnt())

	require.Equal(t, NVIDIAUVM, mods[1].Name)
	require.Empty(t, mods[1].Version)
	require.Equal(t, 0, mods[1].Refcnt())
}
