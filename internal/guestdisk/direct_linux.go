// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package guestdisk

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Read returns the last AreaSize bytes of path. In the guest, path is the
// bundle's block device, whose page cache would keep serving the bytes it read
// first while the host rewrites the disk underneath; O_DIRECT reads the disk.
func Read(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECT, 0)
	if errors.Is(err, unix.EINVAL) {
		// A filesystem without O_DIRECT, such as tmpfs, has no cache to go
		// stale behind a writer on the same host: buffered reads are coherent.
		f, err = os.Open(path)
	}
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

	// O_DIRECT needs a block-aligned buffer; an anonymous mapping is
	// page-aligned.
	buf, err := unix.Mmap(-1, 0, AreaSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, fmt.Errorf("allocate aligned buffer: %w", err)
	}
	defer func() { _ = unix.Munmap(buf) }() // only fails for a bad mapping, which Mmap just returned

	if _, err := f.ReadAt(buf, size-AreaSize); err != nil {
		return nil, fmt.Errorf("read live area of %s: %w", path, err)
	}

	return append([]byte(nil), buf...), nil
}
