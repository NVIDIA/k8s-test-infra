// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysoverlay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

// writeVendor is a stage function writing one attribute into the entry.
func writeVendor(entry string, _ *agent.State) error {
	if err := os.MkdirAll(filepath.Join(entry, "id"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(entry, "id/sys_vendor"), []byte("NVIDIA\n"), 0o444)
}

func TestSimulator_StageWritesTheEntry(t *testing.T) {
	t.Parallel()
	o, _ := testOverlay(t)
	require.NoError(t, os.RemoveAll(o.Staged))
	sim := NewSimulator("dmi", o, writeVendor)

	require.NoError(t, sim.Stage(t.Context(), &agent.State{}))

	require.Equal(t, "NVIDIA\n", readFile(t, filepath.Join(o.StagedEntry(), "id/sys_vendor")))
}

func TestSimulator_LeavesAnEntryTheKernelProvidesAlone(t *testing.T) {
	t.Parallel()
	o, _ := testOverlay(t)
	require.NoError(t, os.RemoveAll(o.Staged))
	require.NoError(t, os.MkdirAll(filepath.Join(o.Dir, o.Entry), 0o755))
	sim := NewSimulator("dmi", o, writeVendor)

	require.NoError(t, sim.Stage(t.Context(), &agent.State{}))
	require.NoError(t, sim.Apply(t.Context(), &agent.State{}))

	require.True(t, sim.Ready())
	require.NoDirExists(t, o.Staged, "nothing is staged where the kernel provides the entry")
}

func TestSimulator_DiscardRemovesTheStagedEntry(t *testing.T) {
	t.Parallel()
	o, _ := testOverlay(t)
	sim := NewSimulator("dmi", o, writeVendor)

	require.NoError(t, sim.Discard(t.Context()))

	require.NoDirExists(t, o.StagedEntry())
}

func TestSimulator_ServesUntilRevoked(t *testing.T) {
	skipUnlessRootLinux(t)
	o, _ := testOverlay(t)
	require.NoError(t, os.RemoveAll(o.Staged))
	sim := NewSimulator("dmi", o, writeVendor)
	withdrawCleanup(t, o)

	require.NoError(t, sim.Stage(t.Context(), &agent.State{}))
	require.NoError(t, sim.Apply(t.Context(), &agent.State{}))
	require.True(t, sim.Ready())
	require.FileExists(t, filepath.Join(o.Dir, "dmi/id/sys_vendor"))

	require.NoError(t, sim.Revoke(t.Context()))
	require.False(t, sim.Ready())
	require.NoDirExists(t, filepath.Join(o.Dir, o.Entry))
}

// Removing the staged tree while it is still served would leave the node an
// empty entry instead of none.
func TestSimulator_DiscardRefusesWhileServed(t *testing.T) {
	skipUnlessRootLinux(t)
	o, _ := testOverlay(t)
	sim := NewSimulator("dmi", o, writeVendor)
	withdrawCleanup(t, o)
	require.NoError(t, sim.Apply(t.Context(), &agent.State{}))

	require.Error(t, sim.Discard(t.Context()))

	require.FileExists(t, filepath.Join(o.Dir, "dmi/id/sys_vendor"))
}
