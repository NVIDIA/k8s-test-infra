// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// skipUnlessNvidiaSMI skips the test when the nvidia-smi ELF is not installed,
// matching skipUnlessNVMLLib's guard for the same reason: the closure is read
// out of a real binary, which only the built image carries.
func skipUnlessNvidiaSMI(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(nvidiaSMISource); err != nil {
		t.Skip("nvidia-smi ELF not installed at " + nvidiaSMISource)
	}
}

func TestResolveSoname_FindsLibraryInMultiarchSubdir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "aarch64-linux-gnu")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	want := filepath.Join(dir, "libc.so.6")
	require.NoError(t, os.WriteFile(want, []byte("elf"), 0o755))

	got, err := resolveSoname("libc.so.6", []string{root})

	require.NoError(t, err)
	require.Equal(t, want, got, "the multiarch subdirectory is found without a GOARCH table")
}

func TestResolveSoname_FindsLibraryDirectlyUnderRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	want := filepath.Join(root, "libm.so.6")
	require.NoError(t, os.WriteFile(want, []byte("elf"), 0o755))

	got, err := resolveSoname("libm.so.6", []string{root})

	require.NoError(t, err)
	require.Equal(t, want, got)
}

// A SONAME that resolves nowhere must fail the staging rather than be skipped:
// a driver root missing one library fails the chroot exec just as completely as
// one missing all of them, and it fails at the consumer instead of here.
func TestResolveSoname_ReportsAMissingLibrary(t *testing.T) {
	t.Parallel()

	_, err := resolveSoname("libnothing.so.9", []string{t.TempDir()})

	require.Error(t, err)
	require.Contains(t, err.Error(), "libnothing.so.9")
}

func TestChrootDest_MapsMultiarchLib(t *testing.T) {
	t.Parallel()

	driverRoot := filepath.Join(t.TempDir(), "driver")
	got, err := chrootDest(driverRoot, "/lib/aarch64-linux-gnu/libc.so.6", true)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(driverRoot, "lib/aarch64-linux-gnu/libc.so.6"), got)
}

func TestChrootDest_StripsUsrLib64(t *testing.T) {
	t.Parallel()

	driverRoot := filepath.Join(t.TempDir(), "driver")
	got, err := chrootDest(driverRoot, "/usr/lib64/libc.so.6", true)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(driverRoot, "lib64/libc.so.6"), got)
	require.NotContains(t, got, filepath.Join("driver", "usr"),
		"a /usr/lib64 library must not land under the CDI-injected driver/usr tree")
	require.NotContains(t, got, "/usr/",
		"destination %s still contains /usr/", got)
}

func TestChrootDest_StripsUsrLibMultiarch(t *testing.T) {
	t.Parallel()

	driverRoot := filepath.Join(t.TempDir(), "driver")
	got, err := chrootDest(driverRoot, "/usr/lib/x86_64-linux-gnu/libm.so.6", true)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(driverRoot, "lib/x86_64-linux-gnu/libm.so.6"), got)
}

func TestChrootDest_RejectsPathOutsideStageRoots(t *testing.T) {
	t.Parallel()

	driverRoot := filepath.Join(t.TempDir(), "driver")
	_, err := chrootDest(driverRoot, "/opt/vendor/libfoo.so", true)

	require.Error(t, err)
	require.Contains(t, err.Error(), "/opt/vendor/libfoo.so")
	require.Contains(t, err.Error(), "outside")
}

func TestChrootDest_InterpreterKeepsLiteralPath(t *testing.T) {
	t.Parallel()

	driverRoot := filepath.Join(t.TempDir(), "driver")
	// remapUSR=false: even a hypothetical /usr-prefixed interpreter stays put.
	got, err := chrootDest(driverRoot, "/lib64/ld-linux-x86-64.so.2", false)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(driverRoot, "lib64/ld-linux-x86-64.so.2"), got)
}

// The interpreter has to land as a real file. cp -a style symlink preservation
// leaves the target outside the root and reproduces the exact ENOENT this
// staging exists to fix. See issue #759.
func TestStageChrootRuntime_StagesTheInterpreterAsARealFile(t *testing.T) {
	t.Parallel()
	skipUnlessNvidiaSMI(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageChrootRuntime(context.Background(), h, state))

	closure, interps, err := chrootRuntimeClosure([]string{nvidiaSMISource}, libSearchRoots)
	require.NoError(t, err)
	require.NotEmpty(t, closure)

	driverRoot := filepath.Join(h.Root, "driver")
	for _, src := range closure {
		staged, err := chrootDest(driverRoot, src, !interps[src])
		require.NoError(t, err, "%s must map into the driver root", src)
		info, err := os.Lstat(staged)
		require.NoError(t, err, "%s must be staged into the driver root", src)
		require.Zero(t, info.Mode()&os.ModeSymlink,
			"%s must be a real file, not a symlink whose target is outside the chroot", staged)
		require.NotZero(t, info.Size(), "%s must not be empty", staged)
	}
}

// The C runtime must stay out of driver/usr/lib64, which internal/agent/cdi
// bind-mounts into consumer containers: a second libc ahead of the container's
// own on the search path hands a binary a libc that does not match its loader.
func TestStageChrootRuntime_KeepsGlibcOutOfTheInjectedLibDir(t *testing.T) {
	t.Parallel()
	skipUnlessNvidiaSMI(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageChrootRuntime(context.Background(), h, state))

	injected := filepath.Join(h.Root, "driver/usr/lib64")
	entries, err := os.ReadDir(injected)
	if os.IsNotExist(err) {
		return
	}
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "libc.so"),
			"staging put %s where CDI injects it into consumer containers", e.Name())
		require.False(t, strings.HasPrefix(e.Name(), "ld-linux"),
			"staging put the loader %s where CDI injects it into consumer containers", e.Name())
	}
}
