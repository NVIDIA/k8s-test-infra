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

	out := ProcModules(ParseProcModules(hostLine), Modules("550.163.01", false))

	require.Contains(t, out, hostLine)
	require.Contains(t, out, "nvidia_uvm 3411968 0 - Live 0x0000000000000000\n")
	require.Contains(t, out, "nvidia 62312448 4 nvidia_uvm,nvidia_modeset,gdrdrv,nvidia_fs, Live 0x0000000000000000\n")
}

func TestProcModules_KeepsHostEntryForAnAlreadyLoadedModule(t *testing.T) {
	t.Parallel()

	src := "nvidia 62312448 7 nvidia_uvm, Live 0x0000000000000000\n"

	out := ProcModules(ParseProcModules(src), Modules("550.163.01", false))

	require.Equal(t, 1, strings.Count(out, "nvidia 62312448"))
	require.Contains(t, out, "7 nvidia_uvm,")
}

func TestProcModules_IdempotentAgainstItsOwnOutput(t *testing.T) {
	t.Parallel()

	mods := Modules("550.163.01", false)

	first := ProcModules(ParseProcModules(hostLine), mods)
	second := ProcModules(ParseProcModules(first), mods)

	require.Equal(t, first, second)
}

func TestProcModules_TerminatesASourceMissingItsFinalNewline(t *testing.T) {
	t.Parallel()

	out := ProcModules(ParseProcModules("xfs 1556480 1 - Live 0x0000000000000000"), Modules("550.163.01", false))

	require.Contains(t, out, "0x0000000000000000\nnvidia")
}

func TestProcModules_RendersEntriesForAnEmptySource(t *testing.T) {
	t.Parallel()

	out := ProcModules(HostModules{}, Modules("550.163.01", false))

	require.Equal(t, 5, strings.Count(out, "\n"), "one line per simulated module")
	require.True(t, strings.HasPrefix(out, "nvidia "))
}

func TestProcModules_KeepsALineTheParserCannotRead(t *testing.T) {
	t.Parallel()

	src := "truncated 1000\n" + hostLine

	out := ProcModules(ParseProcModules(src), Modules("550.163.01", false))

	require.Contains(t, out, "truncated 1000\n",
		"the host text is served back whole, not rebuilt from parsed fields")
	require.Contains(t, out, "nvidia 62312448 4 nvidia_uvm,")
}

func TestParseProcModules_EmptyTextMatchesTheZeroValue(t *testing.T) {
	t.Parallel()

	require.Empty(t, ParseProcModules("").byName)
	require.Equal(t, ProcModules(HostModules{}, Modules("550.163.01", false)),
		ProcModules(ParseProcModules(""), Modules("550.163.01", false)),
		"the zero value must serve what an empty file serves")
}

func TestLoadedModules_ReadsSizeAndRefcntAndIgnoresBlankLines(t *testing.T) {
	t.Parallel()

	mods := loadedModules("xfs 1556480 1 - Live 0x0\n\nnvidia_uvm 3411968 0 - Live 0x0\n")

	require.Len(t, mods, 2)
	require.Equal(t, hostModule{sizeBytes: "1556480", refcnt: "1", holdersKnown: true}, mods["xfs"])
	require.Equal(t, hostModule{sizeBytes: "3411968", refcnt: "0", holdersKnown: true}, mods["nvidia_uvm"])
	require.NotContains(t, mods, "")
}

func TestLoadedModules_SeparatesAnAbsentDependencyFieldFromNone(t *testing.T) {
	t.Parallel()

	mods := loadedModules("xfs 1556480 1 - Live 0x0\nbtrfs 1000 0\n")

	require.True(t, mods["xfs"].holdersKnown, "a dash means the host reported no holders")
	require.Empty(t, mods["xfs"].holders)
	require.False(t, mods["btrfs"].holdersKnown, "a truncated line reports nothing about holders")
}

func TestModules_OnlyNVIDIACarriesTheVersionAndTheHolders(t *testing.T) {
	t.Parallel()

	mods := Modules("550.163.01", true)
	require.NotEmpty(t, mods)

	for _, mod := range mods {
		if mod.Name == NVIDIA {
			require.Equal(t, "550.163.01", mod.Version)
			require.Equal(t, 5, mod.Refcnt())
			continue
		}

		require.Empty(t, mod.Version, "%s must not carry the driver version", mod.Name)
		require.Zero(t, mod.Refcnt(), "%s holds nothing of its own", mod.Name)
	}
}

func TestModules_ServeTheirCapturedSizes(t *testing.T) {
	t.Parallel()

	want := map[string]int{
		NVIDIA:        62312448,
		NVIDIAUVM:     3411968,
		NVIDIAModeset: 2097152,
		GDRDrv:        262144,
		NVIDIAFS:      245760,
		NVIDIAPeermem: 16384,
		MLX5Core:      3145728,
	}

	mods := Modules("550.163.01", true)
	require.Len(t, mods, len(want))

	for _, mod := range mods {
		require.Equal(t, want[mod.Name], mod.SizeBytes, "coresize for %s", mod.Name)
	}
}

func TestModules_ServesTheFabricModulesOnlyWhenIBIsEnabled(t *testing.T) {
	t.Parallel()

	without := moduleNames(Modules("550.163.01", false))
	require.NotContains(t, without, NVIDIAPeermem)
	require.NotContains(t, without, MLX5Core)

	with := moduleNames(Modules("550.163.01", true))
	require.Contains(t, with, NVIDIAPeermem)
	require.Contains(t, with, MLX5Core)
}

func TestModules_NVIDIAGainsPeermemAsAHolderWithIB(t *testing.T) {
	t.Parallel()

	require.NotContains(t, nvidiaHolders(t, false), NVIDIAPeermem)

	holders := nvidiaHolders(t, true)
	require.Contains(t, holders, NVIDIAPeermem)
	require.NotContains(t, holders, MLX5Core)
}

func moduleNames(mods []Module) []string {
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		names = append(names, m.Name)
	}

	return names
}

func nvidiaHolders(t *testing.T, ibEnabled bool) []string {
	t.Helper()

	for _, m := range Modules("550.163.01", ibEnabled) {
		if m.Name == NVIDIA {
			return m.Holders
		}
	}

	t.Fatalf("no %s module in the simulated set", NVIDIA)

	return nil
}
