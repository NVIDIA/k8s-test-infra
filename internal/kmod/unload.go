// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// RefusedError is delete_module refusing a module, with the kernel's errno:
// ENOENT if not loaded, EWOULDBLOCK while held.
type RefusedError struct {
	Errno unix.Errno
}

func (e *RefusedError) Error() string {
	return "kmod unload: " + e.Errno.Error()
}

func (e *RefusedError) Unwrap() error { return e.Errno }

// Unload removes a module from root, a tree laid out as /sys/module, as
// delete_module does, releasing the references it held. A refusal is a
// *RefusedError.
func Unload(root, name string) error {
	if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, "/") {
		return &RefusedError{Errno: unix.ENOENT}
	}

	dir := filepath.Join(root, name)
	if err := unused(dir); err != nil {
		return err
	}
	// initstate first: a removal that fails partway must not leave the module
	// looking live to the operands' gate.
	if err := os.Remove(filepath.Join(dir, "initstate")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("kmod unload: remove %s: %w", name, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("kmod unload: remove %s: %w", name, err)
	}
	// The module is gone, as delete_module reports. unused ignores a hold left
	// by a module that is gone, so a failed release leaves only stale files.
	if err := release(root, name); err != nil {
		zap.L().Warn("kmod unload: references not released", zap.String("module", name), zap.Error(err))
	}
	return nil
}

// unused fails for a module that is not loaded or that something holds.
func unused(dir string) error {
	refcnt, err := readRefcnt(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return &RefusedError{Errno: unix.ENOENT}
	}
	if err != nil {
		return err
	}

	holders, err := os.ReadDir(filepath.Join(dir, "holders"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("kmod unload: read holders of %s: %w", filepath.Base(dir), err)
	}
	for _, holder := range holders {
		_, err := os.Stat(filepath.Join(dir, "holders", holder.Name()))
		if err == nil {
			return &RefusedError{Errno: unix.EWOULDBLOCK}
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("kmod unload: holder %s of %s: %w", holder.Name(), filepath.Base(dir), err)
		}
	}
	// refcnt counts holders too; more means a process holds it.
	if refcnt > len(holders) {
		return &RefusedError{Errno: unix.EWOULDBLOCK}
	}
	return nil
}

// release drops the reference name held on every module it depended on.
func release(root, name string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read %s: %w", root, err)
	}

	var errs []error
	for _, e := range entries {
		if err := releaseOne(filepath.Join(root, e.Name()), name); err != nil {
			errs = append(errs, fmt.Errorf("release %s: %w", e.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// releaseOne drops name's reference on the module in dir, if it holds one.
func releaseOne(dir, name string) error {
	link := filepath.Join(dir, "holders", name)
	if _, err := os.Lstat(link); err != nil {
		// Not a holder of this module, or not a module directory at all.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) {
			return nil
		}
		return err
	}
	// refcnt first: a link left behind still counts against it in unused.
	refcnt, err := readRefcnt(dir)
	if err != nil {
		return err
	}
	if err := fsutil.Write(filepath.Join(dir, "refcnt"), []byte(strconv.Itoa(max(refcnt-1, 0))+"\n"), 0o644); err != nil {
		return err
	}
	return os.Remove(link)
}

// readRefcnt reads a module's refcnt; its presence marks the module loaded.
func readRefcnt(dir string) (int, error) {
	content, err := os.ReadFile(filepath.Join(dir, "refcnt"))
	if err != nil {
		return 0, err
	}
	refcnt, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return 0, fmt.Errorf("kmod unload: refcnt of %s: %w", filepath.Base(dir), err)
	}
	return refcnt, nil
}
