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
	"strings"
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

// PruneDir removes every entry of dir that keep rejects.
func PruneDir(dir string, keep func(string) bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}

	var errs []error
	for _, e := range entries {
		if keep(e.Name()) {
			continue
		}

		p := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
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

// BindMount makes target a bind mount of source: mkdir target if absent
// (never replacing it if not), then bind-mount source onto it. A no-op if
// target is already a mount, so repeated calls are safe.
func BindMount(src, dst string) error {
	mounted, err := IsMounted(dst)
	if err != nil {
		return err
	}
	if mounted {
		return nil
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst, err)
	}

	if err := bindMount(src, dst); err != nil {
		return fmt.Errorf("bind mount %s -> %s: %w", src, dst, err)
	}

	return nil
}

// Unmount lazily detaches target if it is currently a mount point, leaving
// the directory entry itself in place. Not-mounted is not an error.
func Unmount(dst string) error {
	mounted, err := IsMounted(dst)
	if err != nil {
		return err
	}
	if !mounted {
		return nil
	}

	if err := lazyUnmount(dst); err != nil {
		return fmt.Errorf("unmount %s: %w", dst, err)
	}

	return nil
}

// IsMounted reports whether path is itself a mount point, by scanning this
// process's mount table.
func IsMounted(path string) (bool, error) {
	clean := filepath.Clean(path)

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		// mountinfo's 5th whitespace-separated field is the mount point.
		if len(fields) >= 5 && fields[4] == clean {
			return true, nil
		}
	}

	return false, nil
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
