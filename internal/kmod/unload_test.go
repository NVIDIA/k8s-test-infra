// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// loadedTree renders the simulated modules into a fresh tree and returns the
// directory that stands in for /sys/module.
func loadedTree(t *testing.T) string {
	t.Helper()

	overlay := t.TempDir()
	_, err := Render(Options{Modules: Modules("580.0.1", false), OverlayRoot: overlay})
	require.NoError(t, err)
	return filepath.Join(overlay, SysModuleRelPath)
}

func readAttr(t *testing.T, root, module, attr string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(root, module, attr))
	require.NoError(t, err)
	return string(content)
}

// The order k8s-driver-manager unloads in: every holder before nvidia.
func TestUnload_TheDriverManagerOrderEmptiesTheTree(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)

	for _, name := range []string{NVIDIAModeset, NVIDIAUVM, NVIDIAFS, GDRDrv, NVIDIA} {
		require.NoError(t, Unload(root, name), name)
	}

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// The kernel drops a holder's reference when the holder goes, so nvidia's
// refcnt and holders/ follow each unload.
func TestUnload_ReleasesTheModulesItHeld(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)

	require.NoError(t, Unload(root, NVIDIAUVM))

	require.Equal(t, "3\n", readAttr(t, root, NVIDIA, "refcnt"))
	_, err := os.Lstat(filepath.Join(root, NVIDIA, "holders", NVIDIAUVM))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoDirExists(t, filepath.Join(root, NVIDIAUVM))
}

// delete_module refuses a module another module still holds, and leaves it
// loaded.
func TestUnload_RefusesAModuleWithHolders(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)

	require.ErrorIs(t, Unload(root, NVIDIA), unix.EWOULDBLOCK)
	require.Equal(t, "4\n", readAttr(t, root, NVIDIA, "refcnt"))
}

// A reference no holder accounts for is a process with the device open.
func TestUnload_RefusesAModuleAProcessHolds(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, NVIDIAUVM, "refcnt"), []byte("1\n"), 0o644))

	require.ErrorIs(t, Unload(root, NVIDIAUVM), unix.EWOULDBLOCK)
	require.DirExists(t, filepath.Join(root, NVIDIAUVM))
}

func TestUnload_AModuleThatIsNotLoaded(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)

	for _, name := range []string{"nvidia_peermem", "", ".", "..", "../" + NVIDIA, ".unloading-" + NVIDIAUVM} {
		err := Unload(root, name)
		var refused *RefusedError
		require.ErrorAs(t, err, &refused, "%q", name)
		require.Equal(t, unix.ENOENT, refused.Errno, "%q", name)
	}
	require.DirExists(t, filepath.Join(root, NVIDIA))
}

// An aside an earlier unload left behind is not a module, even with a refcnt.
func TestUnload_RefusesAnAside(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	aside := filepath.Join(root, ".unloading-"+NVIDIAUVM)
	require.NoError(t, os.MkdirAll(aside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(aside, "refcnt"), []byte("0\n"), 0o644))

	require.ErrorIs(t, Unload(root, ".unloading-"+NVIDIAUVM), unix.ENOENT)
	require.DirExists(t, aside)
	require.Equal(t, "4\n", readAttr(t, root, NVIDIA, "refcnt"))
}

// A module the unloaded one held but cannot be released does not keep the
// others held, and the module is gone either way, as after delete_module.
func TestUnload_ReleasesTheRestPastABrokenModule(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	broken := filepath.Join(root, "a_broken")
	require.NoError(t, os.MkdirAll(filepath.Join(broken, "holders"), 0o755))
	require.NoError(t, os.Symlink("../../"+NVIDIAUVM, filepath.Join(broken, "holders", NVIDIAUVM)))
	require.NoError(t, os.WriteFile(filepath.Join(broken, "refcnt"), []byte("live\n"), 0o644))

	err := Unload(root, NVIDIAUVM)

	require.ErrorIs(t, err, ErrIncomplete)
	require.NoDirExists(t, filepath.Join(root, NVIDIAUVM))
	require.Equal(t, "3\n", readAttr(t, root, NVIDIA, "refcnt"))
}

// An unload that failed after the module went leaves its directory aside; the
// next unload of that name clears it rather than failing on it.
func TestUnload_ClearsWhatAnEarlierUnloadLeftAside(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".unloading-"+NVIDIAUVM, "holders"), 0o755))

	require.NoError(t, Unload(root, NVIDIAUVM))

	require.NoDirExists(t, filepath.Join(root, ".unloading-"+NVIDIAUVM))
	require.NoDirExists(t, filepath.Join(root, NVIDIAUVM))
	require.Equal(t, "3\n", readAttr(t, root, NVIDIA, "refcnt"))
}
