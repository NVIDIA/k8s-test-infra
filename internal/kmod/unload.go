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

// ErrIncomplete reports a module unloaded without all the references it held
// released.
var ErrIncomplete = errors.New("kmod unload: references not released")

// Unload removes a module from root, a tree laid out as /sys/module, as
// delete_module does, releasing the module's own references. A refusal is a
// *RefusedError; once the module is gone, any later failure wraps
// ErrIncomplete.
func Unload(root, name string) error {
	// A leading dot also covers the asides below, which are not modules.
	if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, "/") {
		return &RefusedError{Errno: unix.ENOENT}
	}

	dir := filepath.Join(root, name)
	if err := unused(dir); err != nil {
		return err
	}
	// Remove the module before releasing its references, so a failure in
	// between cannot leave it loaded with them already released.
	aside := filepath.Join(root, ".unloading-"+name)
	if err := os.RemoveAll(aside); err != nil {
		return fmt.Errorf("kmod unload: clear %s: %w", aside, err)
	}
	if err := os.Rename(dir, aside); err != nil {
		return fmt.Errorf("kmod unload: remove %s: %w", name, err)
	}
	err := errors.Join(release(root, name), os.RemoveAll(aside))
	if err != nil {
		return fmt.Errorf("%w by %s: %w", ErrIncomplete, name, err)
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
	// refcnt counts holders too; more means a process holds it.
	if len(holders) > 0 || refcnt > 0 {
		return &RefusedError{Errno: unix.EWOULDBLOCK}
	}
	return nil
}

// release drops the reference name holds on every module it depends on. It
// goes on past a module it fails to release, so one bad module does not keep
// the others held.
func release(root, name string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("kmod unload: read %s: %w", root, err)
	}

	var errs []error
	for _, e := range entries {
		if err := releaseOne(filepath.Join(root, e.Name()), name); err != nil {
			errs = append(errs, fmt.Errorf("kmod unload: release %s: %w", e.Name(), err))
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
	if err := os.Remove(link); err != nil {
		return err
	}

	refcnt, err := readRefcnt(dir)
	if err != nil {
		return err
	}
	refcnt = max(refcnt-1, 0)
	return fsutil.Write(filepath.Join(dir, "refcnt"), []byte(strconv.Itoa(refcnt)+"\n"), 0o644)
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
