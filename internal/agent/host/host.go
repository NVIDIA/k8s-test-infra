// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package host abstracts on-host filesystem roots so simulators can retarget
// to t.TempDir() in tests without mounting a real /host.
package host

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Host holds the roots for each host filesystem namespace the agent writes to.
// Production: hostPrefix = "/host". Tests: hostPrefix = t.TempDir().
type Host struct {
	Root string // /host/var/lib/nvml-mock — simulator staging area
	Dev  string // /host/dev
	Proc string // /host/proc
	Sys  string // /host/sys
	Etc  string // /host/etc
	Run  string // /host/run
}

// New returns a Host whose paths are rooted under hostPrefix.
func New(hostPrefix string) *Host {
	return &Host{
		Root: filepath.Join(hostPrefix, "var/lib/nvml-mock"),
		Dev:  filepath.Join(hostPrefix, "dev"),
		Proc: filepath.Join(hostPrefix, "proc"),
		Sys:  filepath.Join(hostPrefix, "sys"),
		Etc:  filepath.Join(hostPrefix, "etc"),
		Run:  filepath.Join(hostPrefix, "run"),
	}
}

// WriteFile atomically writes data to path, creating parent directories as needed.
func (h *Host) WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// Symlink creates or replaces a symlink at linkPath pointing to target.
func (h *Host) Symlink(target, linkPath string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(linkPath), err)
	}
	_ = os.Remove(linkPath) // idempotent; not-exist is fine
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", linkPath, target, err)
	}
	return nil
}

// Remove removes path; not-exist is not an error.
func (h *Host) Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// CopyFile copies src to dst with mode perm. dst is written atomically via a
// .tmp sibling; parent directories are created as needed.
// src may be any path (e.g. a container-local binary); dst is typically under h.Root.
func (h *Host) CopyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }() // read-only; close error doesn't affect data integrity

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // already returning the copy error; close error would shadow it
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, dst, err)
	}
	return nil
}

// MkdirAll creates dir and all parent directories.
func (h *Host) MkdirAll(dir string, perm os.FileMode) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

// Mknod creates a character device node at path. Parent directories are created
// as needed. EEXIST is not an error; the node's permissions are always set to 0666.
func (h *Host) Mknod(path string, major, minor uint32) error {
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
