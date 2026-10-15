// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/guestdisk"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// stageSource stages what a bundle is built from: the node agent's overlay and
// the guest program the plugin ships from its own image.
func stageSource(t *testing.T) bundleSource {
	t.Helper()

	program := filepath.Join(t.TempDir(), "nvml-mock-guest")
	require.NoError(t, os.WriteFile(program, []byte("guest-program"), 0o755))

	return bundleSource{OverlayRoot: stageOverlay(t), GuestProgram: program}
}

// stageOverlay writes the slice of the node agent's tree a bundle reads, with
// libnvidia-ml.so.1 a symlink as the agent stages it.
func stageOverlay(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for rel, data := range map[string]string{
		"driver/config/config.yaml":                "version: test\n",
		"driver/usr/lib64/libnvidia-ml.so.580.0.0": "mock-library",
		"driver/usr/bin/nvidia-smi":                "mock-smi",
	} {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	}
	require.NoError(t, os.Symlink("libnvidia-ml.so.580.0.0",
		filepath.Join(root, "driver/usr/lib64/libnvidia-ml.so.1")))

	return root
}

type bundled struct {
	mode int64
	data string
}

func readBundle(t *testing.T, path string) map[string]bundled {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })

	entries := map[string]bundled{}
	archive := tar.NewReader(f)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		require.NoError(t, err)
		data, err := io.ReadAll(archive)
		require.NoError(t, err)
		entries[header.Name] = bundled{mode: header.Mode, data: string(data)}
	}
}

// Extracting at / is the whole guest install, so every entry sits at the path
// the guest's engine and dynamic loader look in.
func TestBundleLaysOutTheGuestAtItsRealPaths(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "c0ffee")
	require.NoError(t, writeBundle(stageSource(t), inject.GuestBundle{Dir: dir, Minors: []int{1, 3}}, selfOwner(), nil))

	require.Equal(t, map[string]bundled{
		"etc/nvml-mock/config.yaml": {0o644,
			"version: test\ndevice_defaults:\n  virtualization:\n    mode: passthrough\n"},
		"etc/nvml-mock/allocation":                {0o644, "1\n3\n"},
		"usr/local/lib64/libnvidia-ml.so.1":       {0o755, "mock-library"},
		"usr/local/bin/nvidia-smi":                {0o755, "mock-smi"},
		"usr/local/bin/nvml-mock-guest":           {0o755, "guest-program"},
		"etc/systemd/system/nvml-mock-guest.service": {0o644, guestUnit},
		"etc/ld.so.conf.d/nvml-mock.conf":         {0o644, "/usr/local/lib64\n"},
		"dev/nvidia1":                             {0o666, ""},
		"dev/nvidia3":                             {0o666, ""},
	}, readBundle(t, filepath.Join(dir, BundleFile)))
}

// The guest's program finds the host's overrides in the last area of the disk,
// behind the archive that tar extracts.
func TestBundleEndsInALiveAreaWithTheHostOverrides(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "c0ffee")
	overrides := []byte("version: 1\nall:\n  temperature: 90\n")
	require.NoError(t, writeBundle(stageSource(t), inject.GuestBundle{Dir: dir, Minors: []int{0}}, selfOwner(), overrides))

	path := filepath.Join(dir, BundleFile)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Zero(t, info.Size()%guestdisk.AreaSize, "the area must end the disk on a block boundary")
	area, err := guestdisk.Read(path)
	require.NoError(t, err)
	payload, _, err := guestdisk.Decode(area)
	require.NoError(t, err)
	require.Equal(t, overrides, payload)
	require.Contains(t, readBundle(t, path), "etc/nvml-mock/config.yaml", "the archive must still extract")
}

func TestBundleWriteRefusesOverridesLargerThanTheArea(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "c0ffee")
	err := writeBundle(stageSource(t), inject.GuestBundle{Dir: dir, Minors: []int{0}}, selfOwner(),
		make([]byte, guestdisk.MaxPayload+1))
	require.ErrorIs(t, err, guestdisk.ErrTooLarge)
	require.NoFileExists(t, filepath.Join(dir, BundleFile))
}

// The engine finds a board's MIG profile table beside its config, so the guest
// needs it there too, or a MIG-capable board loses its partitions.
func TestBundleCarriesTheMIGProfileTable(t *testing.T) {
	t.Parallel()

	src := stageSource(t)
	require.NoError(t, os.WriteFile(filepath.Join(src.OverlayRoot, "driver/config/config.mig.yaml"),
		[]byte("supported_profiles: []\n"), 0o644))
	dir := filepath.Join(t.TempDir(), "c0ffee")
	require.NoError(t, writeBundle(src, inject.GuestBundle{Dir: dir, Minors: []int{0}}, selfOwner(), nil))

	require.Equal(t, bundled{0o644, "supported_profiles: []\n"},
		readBundle(t, filepath.Join(dir, BundleFile))["etc/nvml-mock/config.mig.yaml"])
}

// QEMU opens the disk as the unprivileged qemu user, not as root.
func TestBundleIsWorldReadable(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "c0ffee")
	require.NoError(t, writeBundle(stageSource(t), inject.GuestBundle{Dir: dir, Minors: []int{0}}, selfOwner(), nil))

	info, err := os.Stat(filepath.Join(dir, BundleFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// virt-handler chowns a hostDisk volume to the qemu user unless it already
// belongs to it, and the launcher sees the bundle read-only.
func TestBundleBelongsToTheGivenOwner(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "c0ffee")
	owner := selfOwner()
	require.NoError(t, writeBundle(stageSource(t), inject.GuestBundle{Dir: dir, Minors: []int{0}}, owner, nil))

	for _, path := range []string{dir, filepath.Join(dir, BundleFile)} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		stat, ok := info.Sys().(*syscall.Stat_t)
		require.True(t, ok)
		got := bundleOwner{UID: int(stat.Uid), GID: int(stat.Gid)}
		require.Equal(t, owner, got, path)
	}
}

func TestBundleWriteFailsWhenItCannotBeOwned(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can chown to any owner")
	}

	dir := filepath.Join(t.TempDir(), "c0ffee")
	err := writeBundle(stageSource(t), inject.GuestBundle{Dir: dir, Minors: []int{0}}, kubevirtQEMU, nil)
	require.ErrorIs(t, err, os.ErrPermission)
}

func selfOwner() bundleOwner {
	return bundleOwner{UID: os.Getuid(), GID: os.Getgid()}
}

func TestBundleWriteFailsWithoutTheStagedTree(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "c0ffee")
	src := bundleSource{OverlayRoot: t.TempDir(), GuestProgram: stageSource(t).GuestProgram}
	err := writeBundle(src, inject.GuestBundle{Dir: dir, Minors: []int{0}}, selfOwner(), nil)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoFileExists(t, filepath.Join(dir, BundleFile), "a failed write must not leave a partial disk for QEMU")
}

func TestRemoveBundle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "c0ffee"), 0o755))

	require.NoError(t, removeBundle(root, "c0ffee"))
	require.NoDirExists(t, filepath.Join(root, "c0ffee"))
	require.NoError(t, removeBundle(root, "never-had-one"))
	require.NoError(t, removeBundle(root, ".."))
	require.DirExists(t, root)
}

// containerID builds a container ID shaped like the ones containerd generates.
func containerID(c byte) string {
	return strings.Repeat(string(c), 64)
}

func TestSweepKeepsOnlyLiveContainers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	gone, live := containerID('a'), containerID('b')
	for _, id := range []string{gone, live} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, id), 0o755))
	}

	require.NoError(t, sweepBundles(root, map[string]bool{live: true}))
	require.NoDirExists(t, filepath.Join(root, gone))
	require.DirExists(t, filepath.Join(root, live))
	require.NoError(t, sweepBundles(filepath.Join(root, "missing"), nil))
}

// A guest root pointed at a shared directory such as /run must not lose
// anything the plugin did not create.
func TestSweepLeavesEntriesThatAreNotBundles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	foreign := []string{"containerd", strings.Repeat("A", 64), containerID('c')[:63], containerID('z')}
	for _, name := range foreign {
		require.NoError(t, os.MkdirAll(filepath.Join(root, name), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "foreign.pid"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, containerID('d')), []byte("1"), 0o644))

	require.NoError(t, sweepBundles(root, nil))
	for _, name := range foreign {
		require.DirExists(t, filepath.Join(root, name))
	}
	require.FileExists(t, filepath.Join(root, "foreign.pid"))
	require.FileExists(t, filepath.Join(root, containerID('d')))
}
