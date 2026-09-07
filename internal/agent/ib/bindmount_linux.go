// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// bindReadOnly attaches src over dst. The read-only flag needs its own remount
// on Linux: the kernel takes the per-mount flags from the source superblock on
// a plain bind and ignores MS_RDONLY there.
func bindReadOnly(src, dst string) error {
	err := unix.Mount(src, dst, "", unix.MS_BIND, "")

	switch {
	case err == nil:
	case errors.Is(err, unix.EBUSY):
		// Already attached by an earlier reconcile, which is the steady state:
		// mounting is idempotent from the agent's point of view.
		return nil
	default:
		return fmt.Errorf("bind %s onto %s: %w", src, dst, err)
	}

	if err := unix.Mount("", dst, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("seal %s read-only: %w", dst, err)
	}

	return nil
}

// unmountAt detaches whatever is mounted at dst, tolerating a path that never
// was: Revoke runs on shutdown paths that may not have mounted anything.
func unmountAt(dst string) error {
	err := unix.Unmount(dst, unix.UMOUNT_NOFOLLOW)

	switch {
	case err == nil, errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOENT):
		return nil
	default:
		return fmt.Errorf("unmount %s: %w", dst, err)
	}
}
