// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fsutil_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMountChecksUseCallingThreadNamespace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	require.NoError(t, os.Mkdir(src, 0700))
	require.NoError(t, os.Mkdir(dst, 0700))
	// Keep the calling thread occupied so the worker uses a separate OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		// The altered namespace must never be reused by another Go goroutine.
		result <- checkPrivateMount(src, dst)
	}()
	err := <-result
	if errors.Is(err, unix.EPERM) {
		t.Skip("requires mount namespace and mount privileges")
	}
	require.NoError(t, err)
	mounted, err := fsutil.IsMounted(dst)
	require.NoError(t, err)
	require.False(t, mounted, "the mount must stay private to its owning thread")
}

func checkPrivateMount(src, dst string) (err error) {
	if err := unix.Unshare(unix.CLONE_FS | unix.CLONE_NEWNS); err != nil {
		return err
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	if err := unix.Mount(src, dst, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	defer func() {
		cleanupErr := unix.Unmount(dst, 0)
		if !errors.Is(cleanupErr, unix.EINVAL) {
			err = errors.Join(err, cleanupErr)
		}
	}()
	mounted, err := fsutil.IsBindMounted(src, dst)
	if err != nil {
		return err
	}
	if !mounted {
		return errors.New("mount checks did not see the calling thread's bind mount")
	}
	return fsutil.Unmount(src, dst)
}
