// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// mknodChar creates a character device at path; EEXIST is treated as success (idempotent).
func mknodChar(path string, major, minor uint32) error {
	//nolint:gosec // Mknod requires the cast; values are controlled constants
	err := unix.Mknod(path, uint32(syscall.S_IFCHR)|0o666, int(unix.Mkdev(major, minor)))
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("mknod %s: %w", path, err)
	}
	return nil
}
