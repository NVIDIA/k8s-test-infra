// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fsutil_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// siblings lists the directory entries next to path, which is where a leaked
// temp file would show up.
func siblings(t *testing.T, path string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

func TestWrite_CreatesParentsWithMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a/b/c/attr")

	require.NoError(t, fsutil.Write(path, []byte("hello\n"), 0o644))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(got))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	require.Equal(t, []string{"attr"}, siblings(t, path), "a temp file was left behind")
}

func TestWrite_ReplacesRatherThanTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attr")

	require.NoError(t, fsutil.Write(path, []byte("old"), 0o600))
	before, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, fsutil.Write(path, []byte("new"), 0o644))
	after, err := os.Stat(path)
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
	require.False(t, os.SameFile(before, after), "%s was rewritten in place", path)
	require.Equal(t, os.FileMode(0o644), after.Mode().Perm(), "the new mode must win")
}

func TestCopy_CarriesContentsAndMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	require.NoError(t, os.WriteFile(src, []byte("elf"), 0o600))

	dst := filepath.Join(dir, "nested/dst")
	require.NoError(t, fsutil.Copy(src, dst, 0o755))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "elf", string(got))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// A failed write must leave neither a temp file nor a damaged destination.
func TestCopy_MissingSourceLeavesDestinationIntact(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "attr")
	require.NoError(t, fsutil.Write(dst, []byte("original"), 0o644))

	require.Error(t, fsutil.Copy(filepath.Join(dir, "absent"), dst, 0o755))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "original", string(got))
	require.Equal(t, []string{"attr"}, siblings(t, dst), "a temp file was left behind")
}

// A source that opens but cannot be read fails after the temp file exists,
// which is the only path where cleanup can leak.
func TestCopy_UnreadableSourceCleansUpTemp(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")

	// A directory opens fine and then fails on read.
	require.Error(t, fsutil.Copy(t.TempDir(), dst, 0o644))

	require.NoFileExists(t, dst)
	require.Empty(t, siblings(t, dst), "a temp file was left behind")
}

// Concurrent writers of one path are why the temp name has to be unique: a
// fixed sibling would have them overwrite and rename each other's staging file.
func TestWrite_ConcurrentWritersOfOnePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attr")

	var wg sync.WaitGroup

	errs := make([]error, 8)

	for i := range errs {
		wg.Add(1)

		go func() {
			defer wg.Done()

			errs[i] = fsutil.Write(path, fmt.Appendf(nil, "writer-%d", i), 0o644)
		}()
	}

	wg.Wait()
	require.NoError(t, errsJoin(errs))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(got), "writer-"), "got %q", got)
	require.Equal(t, []string{"attr"}, siblings(t, path), "a temp file was left behind")
}

func errsJoin(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}

func skipUnlessRootLinux(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

func TestMknod_CreatesChardev(t *testing.T) {
	skipUnlessRootLinux(t)

	path := filepath.Join(t.TempDir(), "test-chardev")
	require.NoError(t, fsutil.Mknod(path, 195, 0))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.EqualValues(t, 0o666, fi.Mode().Perm())
}

func TestMknod_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)

	path := filepath.Join(t.TempDir(), "test-chardev")
	require.NoError(t, fsutil.Mknod(path, 195, 0))
	require.NoError(t, fsutil.Mknod(path, 195, 0), "second Mknod must not error")
}

func TestRemove(t *testing.T) {
	t.Run("existing file is removed", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))

		require.NoError(t, fsutil.Remove(p))

		_, err := os.Stat(p)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("absent path is not an error", func(t *testing.T) {
		require.NoError(t, fsutil.Remove(filepath.Join(t.TempDir(), "does-not-exist")))
	})
}

func TestPruneDir_RemovesOnlyWhatKeepRejects(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, fsutil.Write(filepath.Join(dir, "keep"), []byte("x"), 0o644))
	require.NoError(t, fsutil.Write(filepath.Join(dir, "drop"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dropdir/nested"), 0o755))

	require.NoError(t, fsutil.PruneDir(dir, func(name string) bool { return name == "keep" }))

	require.Equal(t, []string{"keep"}, siblings(t, filepath.Join(dir, "keep")))
}

func TestPruneDir_AbsentDirIsNotAnError(t *testing.T) {
	require.NoError(t, fsutil.PruneDir(filepath.Join(t.TempDir(), "never-rendered"), func(string) bool { return false }))
}

func TestSymlink_CreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "nested/link")

	require.NoError(t, fsutil.Symlink("first", link))
	got, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, "first", got)

	require.NoError(t, fsutil.Symlink("second", link), "an existing link must be replaced")
	got, err = os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, "second", got)
}

// bindMountCleanup unmounts target at test end: a bind mount is a real mount
// on the machine, not sandboxed to t.TempDir().
func bindMountCleanup(t *testing.T, source, target string) {
	t.Helper()
	t.Cleanup(func() { _ = fsutil.Unmount(source, target) })
}

func TestBindMount_MakesSourceContentVisibleAtTarget(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, src, dst)

	require.NoError(t, os.WriteFile(filepath.Join(src, "file"), []byte("content"), 0o644))
	require.NoError(t, fsutil.BindMount(src, dst))

	got, err := os.ReadFile(filepath.Join(dst, "file"))
	require.NoError(t, err)
	require.Equal(t, "content", string(got))
}

func TestBindMount_DoesNotReplaceAnExistingDestinationDirectory(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, src, dst)

	before, err := os.Stat(dst)
	require.NoError(t, err)

	require.NoError(t, fsutil.BindMount(src, dst))

	// stat(dst) now resolves through the mount, to src's inode — that's what
	// a bind mount is. Unmount and confirm the same pre-existing directory
	// entry is still underneath it, unchanged.
	require.NoError(t, fsutil.Unmount(src, dst))
	after, err := os.Stat(dst)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "the destination directory entry must not be replaced")
}

func TestBindMount_IdempotentWhenAlreadyMounted(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, src, dst)

	require.NoError(t, fsutil.BindMount(src, dst))
	require.NoError(t, fsutil.BindMount(src, dst), "a second BindMount of the same target must not error or stack another mount")

	mounted, err := fsutil.IsMounted(dst)
	require.NoError(t, err)
	require.True(t, mounted)
}

// TestBindMount_RefusesAForeignMount covers the review finding: a target
// already mounted from somewhere other than source is not ours to claim.
func TestBindMount_RefusesAForeignMount(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	foreignSrc := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, foreignSrc, dst)

	require.NoError(t, fsutil.BindMount(foreignSrc, dst))

	require.Error(t, fsutil.BindMount(src, dst), "a mount from a different source must not be accepted as ours")
}

func TestUnmount_RemovesTheMountButKeepsTheDirectory(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	dst := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(src, "file"), []byte("content"), 0o644))
	require.NoError(t, fsutil.BindMount(src, dst))

	require.NoError(t, fsutil.Unmount(src, dst))

	_, err := os.Stat(dst)
	require.NoError(t, err, "the directory entry must survive Unmount")

	_, err = os.Stat(filepath.Join(dst, "file"))
	require.ErrorIs(t, err, os.ErrNotExist, "src's content must no longer be visible through dst")
}

func TestUnmount_IdempotentWhenNotMounted(t *testing.T) {
	skipUnlessRootLinux(t)

	require.NoError(t, fsutil.Unmount(t.TempDir(), t.TempDir()), "Unmount on a plain directory must not error")
}

// TestUnmount_LeavesAForeignMountAlone covers the review finding: Revoke
// must not detach a mount some other owner put at this path.
func TestUnmount_LeavesAForeignMountAlone(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	foreignSrc := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, foreignSrc, dst)

	require.NoError(t, fsutil.BindMount(foreignSrc, dst))

	require.NoError(t, fsutil.Unmount(src, dst))

	mounted, err := fsutil.IsMounted(dst)
	require.NoError(t, err)
	require.True(t, mounted, "a foreign mount must survive Unmount")
}

// A source that does not exist yet (e.g. Revoke called before Stage ever
// ran) cannot possibly be what a mount was made from; this must not error.
func TestUnmount_LeavesAForeignMountAloneWhenSourceIsAbsent(t *testing.T) {
	skipUnlessRootLinux(t)

	absentSrc := filepath.Join(t.TempDir(), "never-created")
	foreignSrc := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, foreignSrc, dst)

	require.NoError(t, fsutil.BindMount(foreignSrc, dst))

	require.NoError(t, fsutil.Unmount(absentSrc, dst))

	mounted, err := fsutil.IsMounted(dst)
	require.NoError(t, err)
	require.True(t, mounted, "a foreign mount must survive Unmount")
}

func TestIsMounted(t *testing.T) {
	skipUnlessRootLinux(t)

	src := t.TempDir()
	dst := t.TempDir()
	bindMountCleanup(t, src, dst)

	mounted, err := fsutil.IsMounted(dst)
	require.NoError(t, err)
	require.False(t, mounted, "a plain directory is not a mount point")

	require.NoError(t, fsutil.BindMount(src, dst))

	mounted, err = fsutil.IsMounted(dst)
	require.NoError(t, err)
	require.True(t, mounted)
}
