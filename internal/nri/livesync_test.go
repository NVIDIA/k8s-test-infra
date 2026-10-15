// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/guestdisk"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// liveFixture is a staged source and a guest root holding bundles written by
// the sync, like the plugin's after two launchers started.
type liveFixture struct {
	sync    *liveSync
	src     bundleSource
	root    string
	bundles []string
}

func newLiveFixture(t *testing.T) liveFixture {
	t.Helper()

	src := stageSource(t)
	root := t.TempDir()
	sync := newLiveSync(src, root)
	ids := []string{containerID('a'), containerID('b')}
	bundles := make([]string, 0, len(ids))
	for _, id := range ids {
		dir := filepath.Join(root, id)
		require.NoError(t, sync.writeBundle(inject.GuestBundle{Dir: dir, Minors: []int{0}}, selfOwner()))
		bundles = append(bundles, filepath.Join(dir, BundleFile))
	}

	return liveFixture{sync: sync, src: src, root: root, bundles: bundles}
}

func (f liveFixture) setHostOverrides(t *testing.T, data []byte) {
	t.Helper()

	require.NoError(t, os.WriteFile(hostOverridesPath(f.src.OverlayRoot), data, 0o644))
}

func livePayload(t *testing.T, bundle string) []byte {
	t.Helper()

	area, err := guestdisk.Read(bundle)
	require.NoError(t, err)
	payload, _, err := guestdisk.Decode(area)
	require.NoError(t, err)

	return payload
}

func fileInode(t *testing.T, path string) uint64 {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)

	return stat.Ino
}

// QEMU holds each disk open, so the update has to land in the files it has.
func TestLiveSyncRewritesEveryBundleInPlace(t *testing.T) {
	t.Parallel()

	f := newLiveFixture(t)
	inodes := []uint64{fileInode(t, f.bundles[0]), fileInode(t, f.bundles[1])}
	foreign := filepath.Join(f.root, "foreign")
	require.NoError(t, os.MkdirAll(foreign, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(foreign, BundleFile), []byte("not ours"), 0o644))

	overrides := []byte("version: 1\nall:\n  temperature: 90\n")
	f.setHostOverrides(t, overrides)
	require.NoError(t, f.sync.tick())

	for i, bundle := range f.bundles {
		require.Equal(t, overrides, livePayload(t, bundle), bundle)
		require.Equal(t, inodes[i], fileInode(t, bundle), "bundle %s was replaced, not updated", bundle)
	}
	data, err := os.ReadFile(filepath.Join(foreign, BundleFile))
	require.NoError(t, err)
	require.Equal(t, "not ours", string(data))
}

func TestLiveSyncWritesOnlyWhenTheHostOverridesChange(t *testing.T) {
	t.Parallel()

	f := newLiveFixture(t)
	f.setHostOverrides(t, []byte("version: 1\n"))
	require.NoError(t, f.sync.tick())
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, bundle := range f.bundles {
		require.NoError(t, os.Chtimes(bundle, past, past))
	}

	require.NoError(t, f.sync.tick())
	for _, bundle := range f.bundles {
		info, err := os.Stat(bundle)
		require.NoError(t, err)
		require.True(t, info.ModTime().Equal(past), "an unchanged host file rewrote %s", bundle)
	}
}

// The node agent clears the overrides on restart; the guests must follow.
func TestLiveSyncClearsTheAreaWhenTheHostOverridesGo(t *testing.T) {
	t.Parallel()

	f := newLiveFixture(t)
	f.setHostOverrides(t, []byte("version: 1\n"))
	require.NoError(t, f.sync.tick())
	require.NoError(t, os.Remove(hostOverridesPath(f.src.OverlayRoot)))

	require.NoError(t, f.sync.tick())
	require.Empty(t, livePayload(t, f.bundles[0]))
}

func TestLiveSyncReportsOversizedOverridesOncePerChange(t *testing.T) {
	t.Parallel()

	f := newLiveFixture(t)
	f.setHostOverrides(t, []byte("version: 1\n"))
	require.NoError(t, f.sync.tick())
	f.setHostOverrides(t, bytes.Repeat([]byte("x"), guestdisk.MaxPayload+1))

	require.ErrorIs(t, f.sync.tick(), guestdisk.ErrTooLarge)
	require.Equal(t, []byte("version: 1\n"), livePayload(t, f.bundles[0]), "a refused payload must leave the guests as they were")
	require.NoError(t, f.sync.tick())
}

// A bundle from a plugin without the live area ends in archive data.
func TestLiveSyncLeavesABundleWithoutALiveAreaAlone(t *testing.T) {
	t.Parallel()

	f := newLiveFixture(t)
	legacy := filepath.Join(f.root, containerID('c'), BundleFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
	old := bytes.Repeat([]byte("t"), 2*guestdisk.AreaSize)
	require.NoError(t, os.WriteFile(legacy, old, 0o644))

	overrides := []byte("version: 1\n")
	f.setHostOverrides(t, overrides)
	require.ErrorIs(t, f.sync.tick(), guestdisk.ErrInvalid)

	data, err := os.ReadFile(legacy)
	require.NoError(t, err)
	require.Equal(t, old, data)
	require.Equal(t, overrides, livePayload(t, f.bundles[0]), "one bad bundle must not hold the others back")
}

func TestLiveSyncGivesANewBundleTheCurrentOverrides(t *testing.T) {
	t.Parallel()

	f := newLiveFixture(t)
	overrides := []byte("version: 1\nall:\n  draining: true\n")
	f.setHostOverrides(t, overrides)

	dir := filepath.Join(f.root, containerID('d'))
	require.NoError(t, f.sync.writeBundle(inject.GuestBundle{Dir: dir, Minors: []int{1}}, selfOwner()))
	require.Equal(t, overrides, livePayload(t, filepath.Join(dir, BundleFile)))
}
