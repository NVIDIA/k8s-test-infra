// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fsutil

import "golang.org/x/sys/unix"

func bindMount(src, dst string) error {
	return unix.Mount(src, dst, "", unix.MS_BIND, "")
}

func lazyUnmount(dst string) error {
	return unix.Unmount(dst, unix.MNT_DETACH)
}
