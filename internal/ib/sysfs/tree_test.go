// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/stretchr/testify/require"
)

// renderCount renders hcaCount HCAs into dir with an otherwise fixed spec, so a
// second call differs from the first only in how many HCAs it declares.
func renderCount(t *testing.T, dir string, hcaCount int) {
	t.Helper()
	require.NoError(t, Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: hcaCount},
		RootDir:  dir,
		NodeName: "node-0",
	}))
}

// requireGone asserts nothing whatsoever sits at path. testify's NoDirExists
// lstats and is satisfied by a symlink, so it cannot tell a retracted class
// entry from a stale one still pointing at a departed HCA.
func requireGone(t *testing.T, path string) {
	t.Helper()
	_, err := os.Lstat(path)
	require.True(t, os.IsNotExist(err), "%s still exists (lstat err: %v)", path, err)
}

func TestRender_PrunesDepartedHCAs(t *testing.T) {
	dir := t.TempDir()
	renderCount(t, dir, 4)
	renderCount(t, dir, 2)

	for i := range 2 {
		n := strconv.Itoa(i)
		require.FileExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_"+n+"/node_guid"))
		require.DirExists(t, filepath.Join(dir, "sys/class/infiniband_mad/umad"+n))
		require.FileExists(t, filepath.Join(dir, "dev/infiniband/uverbs"+n))
	}

	for i := 2; i < 4; i++ {
		n := strconv.Itoa(i)
		// Both halves of a departed HCA have to go: the class entry a consumer
		// enumerates, and the directory it named.
		requireGone(t, filepath.Join(dir, "sys/class/infiniband/mlx5_"+n))
		requireGone(t, filepath.Join(dir, ibDevicesRel, "mlx5_"+n))
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband_mad/umad"+n))
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband_mad/issm"+n))
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband_verbs/uverbs"+n))
		require.NoFileExists(t, filepath.Join(dir, "dev/infiniband/umad"+n))
	}
}

func TestRender_DoesNotPublishClassEntryBeforeHCAComplete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	badAttr := filepath.Join(dir, ibDevicesRel, "mlx5_0", "node_type")
	require.NoError(t, os.MkdirAll(badAttr, 0o755))

	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: 1},
		RootDir:  dir,
		NodeName: "node-0",
	})
	require.Error(t, err)

	classEntry := filepath.Join(dir, "sys/class/infiniband/mlx5_0")
	_, statErr := os.Lstat(classEntry)
	require.True(t, os.IsNotExist(statErr), "class entry %s was published for an incomplete HCA: %v", classEntry, statErr)
}

// A profile that turns InfiniBand off has to take the HCAs a previous profile
// rendered with it: the tree sits on a host mount that outlives the edit.
func TestRender_DisabledRetractsExistingTree(t *testing.T) {
	dir := t.TempDir()
	renderCount(t, dir, 2)
	require.FileExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_0/node_guid"))

	require.NoError(t, Render(Options{IB: config.Infiniband{Enabled: false}, RootDir: dir}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "disabling IB must retract the whole tree")
	require.DirExists(t, dir, "the root itself stays, masking any real host IB")
}

// An unchanged attribute must not be rewritten at all: workload processes read
// these files through the LD_PRELOAD shims and cannot be paused.
func TestRender_LeavesUnchangedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	attr := filepath.Join(dir, "sys/class/infiniband/mlx5_0/fw_ver")

	renderCount(t, dir, 2)

	// Backdating is what makes a needless rewrite visible: an in-place truncate
	// keeps the inode, so only the timestamp gives it away.
	stale := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(attr, stale, stale))

	renderCount(t, dir, 2)

	after, err := os.Stat(attr)
	require.NoError(t, err)
	require.WithinDuration(t, stale, after.ModTime(), time.Second,
		"re-rendering an identical spec rewrote %s", attr)
}

func TestRender_SwapsChangedAttribute(t *testing.T) {
	dir := t.TempDir()
	attr := filepath.Join(dir, "sys/class/infiniband/mlx5_0/fw_ver")
	render := func(fw string) {
		require.NoError(t, Render(Options{
			IB:       config.Infiniband{Enabled: true, HCACountOverride: 1, FWVersion: fw},
			RootDir:  dir,
			NodeName: "node-0",
		}))
	}

	render("28.39.2048")
	before, err := os.Stat(attr)
	require.NoError(t, err)

	render("28.40.1000")
	after, err := os.Stat(attr)
	require.NoError(t, err)

	contents, err := os.ReadFile(attr)
	require.NoError(t, err)
	require.Equal(t, "28.40.1000\n", string(contents))
	// A rename swaps the inode; truncate-and-rewrite would keep it, and with it
	// the window where a reader sees an empty attribute.
	require.False(t, os.SameFile(before, after), "%s was rewritten in place", attr)
	require.Equal(t, os.FileMode(0o644), after.Mode().Perm())
}

// A spec that fails validation must leave the running tree alone rather than
// prune it to nothing.
func TestRender_KeepsTreeWhenSpecInvalid(t *testing.T) {
	dir := t.TempDir()
	renderCount(t, dir, 2)

	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: 2, GUIDPrefix: "nothex"},
		RootDir:  dir,
		NodeName: "node-0",
	})
	require.Error(t, err)
	require.FileExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_0/node_guid"))
	require.FileExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_1/node_guid"))
}

// A tree rendered before class entries became symlinks holds real directories
// where the links belong. A reconcile has to convert them, or consumers that
// enumerate the class by skipping directories keep seeing no devices.
func TestRender_ReplacesClassDirectoryWithLink(t *testing.T) {
	dir := t.TempDir()
	class := filepath.Join(dir, "sys/class/infiniband/mlx5_0")
	require.NoError(t, os.MkdirAll(class, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(class, "node_guid"), []byte("stale\n"), 0o644))

	renderCount(t, dir, 1)

	info, err := os.Lstat(class)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink, "%s is still a directory", class)

	guid, err := os.ReadFile(filepath.Join(class, "node_guid"))
	require.NoError(t, err)
	require.NotEqual(t, "stale\n", string(guid), "attributes still come from the retired directory")
}

// Editing an attribute must not churn the class entry: a consumer that resolved
// the link and holds the HCA open would be left reading a detached directory.
func TestRender_KeepsClassLinkAcrossAttributeChange(t *testing.T) {
	dir := t.TempDir()
	class := filepath.Join(dir, "sys/class/infiniband/mlx5_0")
	render := func(fw string) {
		require.NoError(t, Render(Options{
			IB:       config.Infiniband{Enabled: true, HCACountOverride: 1, FWVersion: fw},
			RootDir:  dir,
			NodeName: "node-0",
		}))
	}

	render("28.39.2048")
	before, err := os.Lstat(class)
	require.NoError(t, err)

	render("28.40.1000")
	after, err := os.Lstat(class)
	require.NoError(t, err)

	require.True(t, os.SameFile(before, after), "%s was recreated", class)
}

// A crash between the rename and the delete leaves a staging directory behind;
// the next pass has to reap it rather than accumulate.
func TestRender_ReapsAbandonedStagingDir(t *testing.T) {
	dir := t.TempDir()
	renderCount(t, dir, 2)

	abandoned := filepath.Join(dir, staleDirPrefix+"abandoned")
	require.NoError(t, os.MkdirAll(filepath.Join(abandoned, "sys_class_infiniband_mlx5_9"), 0o755))

	renderCount(t, dir, 2)
	require.NoDirExists(t, abandoned)
}
