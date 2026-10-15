// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/guestdisk"
)

// guestDisk stands in for the bundle disk: an archive, padding and an area.
func guestDisk(t *testing.T, payload []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "disk")
	archive := bytes.Repeat([]byte("a"), 3000)
	area, err := guestdisk.Encode(payload)
	require.NoError(t, err)
	disk := append(append(archive, make([]byte, guestdisk.Pad(int64(len(archive))))...), area...)
	require.NoError(t, os.WriteFile(path, disk, 0o644))

	return path
}

func setArea(t *testing.T, disk string, payload []byte) {
	t.Helper()

	area, err := guestdisk.Encode(payload)
	require.NoError(t, err)
	f, err := os.OpenFile(disk, os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, guestdisk.WriteAt(f, area))
	require.NoError(t, f.Close())
}

func newTestApplier(t *testing.T, payload []byte) *applier {
	t.Helper()

	return &applier{disk: guestDisk(t, payload), overrides: filepath.Join(t.TempDir(), "nvml-mock", "overrides.yaml")}
}

func TestApplyCopiesTheHostOverrides(t *testing.T) {
	t.Parallel()

	payload := []byte("version: 1\nall:\n  temperature: 90\n")
	a := newTestApplier(t, payload)

	changed, err := a.apply()
	require.NoError(t, err)
	require.True(t, changed)
	got, err := os.ReadFile(a.overrides)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// The --once pass and the loop are separate processes; the loop must not
// rewrite what the first pass already applied.
func TestApplyChangesNothingWhenTheGuestAlreadyHasTheOverrides(t *testing.T) {
	t.Parallel()

	a := newTestApplier(t, []byte("version: 1\n"))
	_, err := a.apply()
	require.NoError(t, err)

	changed, err := a.apply()
	require.NoError(t, err)
	require.False(t, changed)
	fresh := &applier{disk: a.disk, overrides: a.overrides}
	changed, err = fresh.apply()
	require.NoError(t, err)
	require.False(t, changed)
}

// The host is the source of truth: its next change replaces a guest-side edit.
func TestApplyReplacesAGuestEditOnTheNextHostChange(t *testing.T) {
	t.Parallel()

	a := newTestApplier(t, []byte("version: 1\n"))
	_, err := a.apply()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(a.overrides, []byte("guest edit\n"), 0o644))

	setArea(t, a.disk, []byte("version: 1\nall:\n  draining: true\n"))
	changed, err := a.apply()
	require.NoError(t, err)
	require.True(t, changed)
	got, err := os.ReadFile(a.overrides)
	require.NoError(t, err)
	require.Equal(t, "version: 1\nall:\n  draining: true\n", string(got))
}

func TestApplyRemovesTheOverridesWhenTheHostHasNone(t *testing.T) {
	t.Parallel()

	a := newTestApplier(t, []byte("version: 1\n"))
	_, err := a.apply()
	require.NoError(t, err)

	setArea(t, a.disk, nil)
	changed, err := a.apply()
	require.NoError(t, err)
	require.True(t, changed)
	require.NoFileExists(t, a.overrides)
}

func TestApplyLeavesTheOverridesOnAnInvalidArea(t *testing.T) {
	t.Parallel()

	a := newTestApplier(t, []byte("version: 1\n"))
	_, err := a.apply()
	require.NoError(t, err)
	f, err := os.OpenFile(a.disk, os.O_RDWR, 0)
	require.NoError(t, err)
	info, err := f.Stat()
	require.NoError(t, err)
	_, err = f.WriteAt([]byte{0xff}, info.Size()-guestdisk.AreaSize+guestdisk.HeaderSize)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = a.apply()
	require.ErrorIs(t, err, guestdisk.ErrInvalid)
	got, err := os.ReadFile(a.overrides)
	require.NoError(t, err)
	require.Equal(t, "version: 1\n", string(got))
}

// nvml-mock-ctl and the NVML setters in the guest take the same lock.
func TestApplyWaitsForTheOverrideLock(t *testing.T) {
	t.Parallel()

	a := newTestApplier(t, []byte("version: 1\n"))
	unlock, err := fsutil.LockSibling(a.overrides)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := a.apply()
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("apply wrote while another writer held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("apply never took the lock")
	}
}
