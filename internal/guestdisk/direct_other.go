// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package guestdisk

import (
	"fmt"
	"io"
	"os"
)

// Read returns the last AreaSize bytes of path. Guests run Linux; this build
// exists so the package's tests run on a developer's machine.
func Read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only; a close error loses nothing

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("size of %s: %w", path, err)
	}
	if size < AreaSize {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrInvalid, path, size)
	}
	buf := make([]byte, AreaSize)
	if _, err := f.ReadAt(buf, size-AreaSize); err != nil {
		return nil, fmt.Errorf("read live area of %s: %w", path, err)
	}

	return buf, nil
}
