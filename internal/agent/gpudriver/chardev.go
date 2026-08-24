// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// mknodChar creates a character device at path with mode 0666; EEXIST is treated
// as success (idempotent). os.Chmod is called unconditionally so nodes left by a
// prior run with the wrong mode (umask-clamped) are repaired on re-entry.
// Mode 0666 matches NVreg_DeviceFileMode: 438 advertised in /proc/driver/nvidia/params.
func mknodChar(path string, major, minor uint32) error {
	//nolint:gosec // Mknod requires the cast; values are controlled constants
	err := unix.Mknod(path, uint32(syscall.S_IFCHR)|0o666, int(unix.Mkdev(major, minor)))
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("mknod %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
