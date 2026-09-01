// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package fsutil holds the filesystem mutations the mock trees are staged with.
// Workloads read those trees through LD_PRELOAD shims at arbitrary moments and
// cannot be paused, so every operation here is safe to repeat and safe to
// observe mid-flight: Write and Copy land in a single rename, Symlink and Remove
// tolerate whatever a previous pass left, and Mknod tolerates an existing node.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Write replaces path with data, creating parent directories as needed.
func Write(path string, data []byte, perm os.FileMode) error {
	return replace(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)

		return err
	})
}

// Copy replaces dst with src's contents, creating parent directories as needed.
// src may be any path; only dst gets the atomicity guarantee.
func Copy(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }() // read-only; a close error cannot corrupt dst

	return replace(dst, perm, func(w io.Writer) error {
		_, err := io.Copy(w, in)

		return err
	})
}

// replace stages the contents in a sibling temp file and renames it over path.
// The temp name is unique so concurrent writers of one path cannot collide, and
// dot-prefixed so a reader listing the directory mid-write does not see it.
func replace(path string, perm os.FileMode, fill func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}

	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once the rename lands

	if err := fill(f); err != nil {
		_ = f.Close()

		return fmt.Errorf("write %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}

	// CreateTemp opens 0600 regardless of the mode the caller asked for.
	if err := os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}

	return nil
}

// Symlink creates or replaces a symlink at linkPath pointing to target.
func Symlink(target, linkPath string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(linkPath), err)
	}

	_ = os.Remove(linkPath) // idempotent; not-exist is fine

	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", linkPath, target, err)
	}

	return nil
}

// Remove removes path; not-exist is not an error. It does not recurse, so a
// non-empty directory is still refused.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	return nil
}

// Mknod creates a character device node at path. Parent directories are created
// as needed. EEXIST is not an error; the node's permissions are always set to 0666.
func Mknod(path string, major, minor uint32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

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
