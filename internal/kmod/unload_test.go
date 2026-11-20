// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"os"
	"path/filepath"
	"strconv"
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

// k8s-driver-manager's unloadDriver deletes these in this order, skipping
// those not loaded. They must empty what the driver stand-in loads: nvidia
// held by nvidia_uvm and nvidia_modeset.
func TestUnload_TheDriverManagerOrderEmptiesTheTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for name, holders := range map[string][]string{NVIDIA: {NVIDIAUVM, NVIDIAModeset}, NVIDIAUVM: nil, NVIDIAModeset: nil} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, name, "holders"), 0o755))
		for _, holder := range holders {
			require.NoError(t, os.Symlink("../../"+holder, filepath.Join(root, name, "holders", holder)))
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, name, "refcnt"), []byte(strconv.Itoa(len(holders))+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, name, "initstate"), []byte("live\n"), 0o644))
	}

	for _, name := range []string{NVIDIAModeset, NVIDIAUVM, NVIDIAPeermem, NVIDIAFS, "nvidia_vgpu_vfio", GDRDrv, NVIDIA} {
		if _, err := os.Stat(filepath.Join(root, name, "refcnt")); err == nil {
			require.NoError(t, Unload(root, name), name)
		}
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

	for _, name := range []string{"nvidia_peermem", "", ".", "..", "../" + NVIDIA} {
		err := Unload(root, name)
		var refused *RefusedError
		require.ErrorAs(t, err, &refused, "%q", name)
		require.Equal(t, unix.ENOENT, refused.Errno, "%q", name)
	}
	require.DirExists(t, filepath.Join(root, NVIDIA))
}

// A module that cannot be released does not keep the others held, and the
// unload succeeds: the module is gone, as after delete_module.
func TestUnload_ReleasesTheRestPastABrokenModule(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	broken := filepath.Join(root, "a_broken")
	require.NoError(t, os.MkdirAll(filepath.Join(broken, "holders"), 0o755))
	require.NoError(t, os.Symlink("../../"+NVIDIAUVM, filepath.Join(broken, "holders", NVIDIAUVM)))
	require.NoError(t, os.WriteFile(filepath.Join(broken, "refcnt"), []byte("live\n"), 0o644))

	require.NoError(t, Unload(root, NVIDIAUVM))

	require.NoDirExists(t, filepath.Join(root, NVIDIAUVM))
	require.Equal(t, "3\n", readAttr(t, root, NVIDIA, "refcnt"))
}

// A hold left by a module that is gone, as after a failed release, does not
// keep the module loaded.
func TestUnload_IgnoresTheHoldOfAModuleThatIsGone(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	for _, name := range []string{NVIDIAModeset, NVIDIAUVM, NVIDIAFS, GDRDrv} {
		require.NoError(t, Unload(root, name), name)
	}
	require.NoError(t, os.Symlink("../../gone", filepath.Join(root, NVIDIA, "holders", "gone")))
	require.NoError(t, os.WriteFile(filepath.Join(root, NVIDIA, "refcnt"), []byte("1\n"), 0o644))

	require.NoError(t, Unload(root, NVIDIA))
}

// A holder that cannot be read may still hold the module: the unload fails
// rather than going ahead.
func TestUnload_FailsOnAnUnreadableHolder(t *testing.T) {
	t.Parallel()

	root := loadedTree(t)
	require.NoError(t, os.Symlink("loop", filepath.Join(root, NVIDIAUVM, "holders", "loop")))

	err := Unload(root, NVIDIAUVM)

	require.Error(t, err)
	var refused *RefusedError
	require.NotErrorAs(t, err, &refused)
	require.DirExists(t, filepath.Join(root, NVIDIAUVM))
}
