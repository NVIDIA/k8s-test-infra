// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fsutil

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// LockSibling takes an exclusive flock on path's sibling ".lock" file, so every
// read-modify-write of path serialises with the others, and returns the unlock.
// The lock file is separate from path because path is replaced by rename.
func LockSibling(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		_ = lf.Close() // the lock was never held; the error above is what matters
		return nil, err
	}

	return func() {
		// Closing the descriptor releases the flock even if LOCK_UN failed.
		_ = unix.Flock(int(lf.Fd()), unix.LOCK_UN)
		_ = lf.Close()
	}, nil
}
