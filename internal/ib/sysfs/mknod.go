// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// makeCharDevice creates one device node. Indirected so a test can assert the
// numbers a render asks for without the privilege mknod needs.
var makeCharDevice = mknodCharDevice

// mknodCharDevice creates a character device, degrading to an empty regular
// file where the caller lacks CAP_MKNOD. The agent's DaemonSet grants it, so
// the fallback covers unprivileged contexts — test runs, sandboxes, a
// developer's laptop — where a device node cannot be created and nothing would
// open it anyway. The rest of the tree still renders there, which is what
// those contexts exercise.
func mknodCharDevice(path string, major, minor uint32) error {
	err := unix.Mknod(path, unix.S_IFCHR|0o666, int(unix.Mkdev(major, minor)))

	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES), errors.Is(err, unix.ENOSYS):
		if writeErr := os.WriteFile(path, nil, 0o644); writeErr != nil {
			return fmt.Errorf("placeholder for %s (mknod: %v): %w", path, err, writeErr)
		}

		return nil
	default:
		return fmt.Errorf("mknod %s: %w", path, err)
	}
}
