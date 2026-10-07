// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysoverlay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

// A container that started while the entry was served holds its own copy of
// the overmount, with the directory's own entries bound onto the staged
// placeholders. Removing a placeholder detaches that entry from the container
// — in another mount namespace silently — so a restarted agent would take the
// node's real devices away from every such pod.
func TestSimulator_DiscardKeepsTheDirectorysOwnEntriesInACopy(t *testing.T) {
	skipUnlessRootLinux(t)
	o, nic := testOverlay(t)
	require.NoError(t, os.RemoveAll(o.Staged))
	sim := NewSimulator("dmi", o, writeVendor)
	withdrawCleanup(t, o)
	require.NoError(t, sim.Stage(t.Context(), &agent.State{}))
	require.NoError(t, sim.Apply(t.Context(), &agent.State{}))

	// What a hostPath mount of /sys gives a container started now.
	copyDir := filepath.Join(t.TempDir(), "container-view")
	require.NoError(t, os.MkdirAll(copyDir, 0o755))
	require.NoError(t, unix.Mount(o.Dir, copyDir, "", unix.MS_BIND|unix.MS_REC, ""))
	t.Cleanup(func() { _ = unix.Unmount(copyDir, unix.MNT_DETACH) })
	require.NoError(t, unix.Mount("", copyDir, "", unix.MS_PRIVATE|unix.MS_REC, ""))

	require.NoError(t, sim.Revoke(t.Context()))
	require.NoError(t, sim.Discard(t.Context()))

	rel, err := filepath.Rel(o.Dir, nic)
	require.NoError(t, err)
	require.Equal(t, readFile(t, nic), readFile(t, filepath.Join(copyDir, rel)),
		"the container must keep the directory's own entries")
	require.NoDirExists(t, filepath.Join(copyDir, o.Entry), "the simulated entry is withdrawn")
}
