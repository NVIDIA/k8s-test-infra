// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// InMountNamespace executes a node operation from the agent, with the node's
// mount namespace and filesystem root. No host-side setup process is needed.
func (h *Host) InMountNamespace(run func(*Host) error) error {
	return runOnDisposableThread(func() error { return h.runInMountNamespace(run) })
}

func runOnDisposableThread(run func() error) error {
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		// Do not unlock: Go destroys this thread on goroutine exit. Its namespaces
		// and filesystem root must never be reused for unrelated agent work.
		result <- run()
	}()
	return <-result
}

func (h *Host) runInMountNamespace(run func(*Host) error) error {
	ns, err := os.Open(h.ProcPath("1/ns/mnt"))
	if err != nil {
		return fmt.Errorf("open node mount namespace: %w", err)
	}
	defer func() { _ = ns.Close() }() // Read-only namespace descriptor.
	root, err := os.Open(h.ProcPath("1/root"))
	if err != nil {
		return fmt.Errorf("open node root: %w", err)
	}
	defer func() { _ = root.Close() }() // Read-only root descriptor.
	// setns requires an unshared filesystem context; chroot must also remain
	// local to this disposable thread rather than changing the agent's root.
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare node filesystem context: %w", err)
	}
	if err := unix.Setns(int(ns.Fd()), unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("enter node mount namespace: %w", err)
	}
	if err := unix.Fchdir(int(root.Fd())); err != nil {
		return err
	}
	if err := unix.Chroot("."); err != nil {
		return err
	}
	if err := unix.Chdir("/"); err != nil {
		return err
	}
	return run(New("/"))
}
