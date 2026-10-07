// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysoverlay

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// testOverlay returns an overlay adding "dmi" to a fake /sys/devices/virtual
// that already holds a device of its own, the kind the overmount must keep.
func testOverlay(t *testing.T) (Overlay, string) {
	t.Helper()
	root := t.TempDir()
	o := Overlay{
		Dir:    filepath.Join(root, "sys/devices/virtual"),
		Staged: filepath.Join(root, "staged"),
		Entry:  "dmi",
	}
	nic := filepath.Join(o.Dir, "net/eth0/address")
	require.NoError(t, os.MkdirAll(filepath.Dir(nic), 0o755))
	require.NoError(t, os.WriteFile(nic, []byte("02:42:ac:12:00:02\n"), 0o444))
	require.NoError(t, os.MkdirAll(filepath.Join(o.StagedEntry(), "id"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(o.StagedEntry(), "id/sys_vendor"), []byte("NVIDIA\n"), 0o444))
	return o, nic
}

// The staged names are what earlier agents left on the node, so they must not
// drift: a restarted agent recognizes its own overmount by them.
func TestAt_NamesTheEntryByItsKernelPath(t *testing.T) {
	t.Parallel()
	h := host.New("/host")

	for relPath, want := range map[string]Overlay{
		"devices/virtual/dmi": {
			Dir:    "/host/sys/devices/virtual",
			Staged: "/host/var/lib/nvml-mock/sys-devices-virtual",
			Entry:  "dmi",
		},
		"bus/node": {
			Dir:    "/host/sys/bus",
			Staged: "/host/var/lib/nvml-mock/sys-bus",
			Entry:  "node",
		},
	} {
		t.Run(relPath, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, At(h, relPath))
		})
	}
}

func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires root on Linux (mount)")
	}
}

// withdrawCleanup detaches the overmount at test end: Serve's mount is a real
// mount on the test machine, not sandboxed to t.TempDir().
func withdrawCleanup(t *testing.T, o Overlay) {
	t.Helper()
	t.Cleanup(func() { _ = o.Withdraw() })
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestLook_AbsentWhenTheKernelProvidesNoEntry(t *testing.T) {
	t.Parallel()
	o, _ := testOverlay(t)

	v, err := o.Look()

	require.NoError(t, err)
	require.Equal(t, Absent, v)
}

func TestLook_KernelWhenTheEntryIsNotTheStagedOne(t *testing.T) {
	t.Parallel()
	o, _ := testOverlay(t)
	require.NoError(t, os.MkdirAll(filepath.Join(o.Dir, o.Entry), 0o755))

	v, err := o.Look()

	require.NoError(t, err)
	require.Equal(t, Kernel, v)
}

func TestServe_RefusesAnEntryTheKernelProvides(t *testing.T) {
	t.Parallel()
	o, _ := testOverlay(t)
	require.NoError(t, os.MkdirAll(filepath.Join(o.Dir, o.Entry), 0o755))

	require.Error(t, o.Serve())
}

func TestServe_AddsTheEntryAndKeepsTheDirectorysOwn(t *testing.T) {
	skipUnlessRootLinux(t)
	o, nic := testOverlay(t)
	withdrawCleanup(t, o)

	require.NoError(t, o.Serve())

	v, err := o.Look()
	require.NoError(t, err)
	require.Equal(t, Served, v)
	require.Equal(t, "NVIDIA\n", readFile(t, filepath.Join(o.Dir, "dmi/id/sys_vendor")))
	require.Equal(t, "02:42:ac:12:00:02\n", readFile(t, nic),
		"the directory's own entries must stay visible under the overmount")
}

// A non-directory entry cannot keep its place under the overmount, and hiding
// what the kernel shows is worse than not serving at all.
func TestServe_RefusesADirectoryHoldingAFile(t *testing.T) {
	skipUnlessRootLinux(t)
	o, _ := testOverlay(t)
	withdrawCleanup(t, o)
	require.NoError(t, os.WriteFile(filepath.Join(o.Dir, "uevent"), nil, 0o644))

	require.Error(t, o.Serve())

	v, err := o.Look()
	require.NoError(t, err)
	require.Equal(t, Absent, v, "a refused serve must leave nothing mounted")
}

// A second Serve that mounted again would stack a second overmount, which one
// Withdraw would not remove.
func TestServe_IsIdempotent(t *testing.T) {
	skipUnlessRootLinux(t)
	o, nic := testOverlay(t)
	withdrawCleanup(t, o)

	require.NoError(t, o.Serve())
	require.NoError(t, o.Serve())
	require.NoError(t, o.Withdraw())

	require.NoDirExists(t, filepath.Join(o.Dir, o.Entry))
	require.FileExists(t, nic)
}

func TestWithdraw_RestoresTheDirectorysOwnView(t *testing.T) {
	skipUnlessRootLinux(t)
	o, nic := testOverlay(t)
	withdrawCleanup(t, o)
	require.NoError(t, o.Serve())

	require.NoError(t, o.Withdraw())

	v, err := o.Look()
	require.NoError(t, err)
	require.Equal(t, Absent, v)
	require.Equal(t, "02:42:ac:12:00:02\n", readFile(t, nic))
}

func TestWithdraw_LeavesAnUnservedDirectoryAlone(t *testing.T) {
	t.Parallel()
	o, nic := testOverlay(t)

	require.NoError(t, o.Withdraw())

	require.FileExists(t, nic)
}
