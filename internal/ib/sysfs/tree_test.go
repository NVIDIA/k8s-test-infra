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
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_"+n))
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband_mad/umad"+n))
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband_mad/issm"+n))
		require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband_verbs/uverbs"+n))
		require.NoFileExists(t, filepath.Join(dir, "dev/infiniband/umad"+n))
	}
}

// A profile that turns InfiniBand off has to take the HCAs a previous profile
// rendered with it: the tree sits on a host mount that outlives the edit.
func TestRender_DisabledRetractsExistingTree(t *testing.T) {
	dir := t.TempDir()
	renderCount(t, dir, 2)
	require.DirExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_0"))

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
