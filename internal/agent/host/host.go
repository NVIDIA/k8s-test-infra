// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package host abstracts on-host filesystem roots so simulators can retarget
// to t.TempDir() in tests without mounting a real /host.
package host

import (
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.Remove(path); !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
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
