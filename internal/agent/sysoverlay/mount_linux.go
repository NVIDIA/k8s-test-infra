// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysoverlay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// overmount mounts staged over dir, then binds each of dir's own entries back
// over a placeholder, so dir gains staged's extra entries and loses none. An
// entry the kernel adds to dir later stays hidden until detach.
//
// Once covered, dir's entries are reachable only through a descriptor opened
// beforehand: the kernel follows /proc/self/fd/N to the covered directory, not
// to whatever is now mounted at its path. That is also why the rebinds go
// through mount(2) directly — mount(8) canonicalizes the source into the path
// and so would bind the placeholders onto themselves.
func overmount(staged, dir string) error {
	covered, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	// Closed only after the rebinds: they resolve through this descriptor.
	defer func() { _ = covered.Close() }()

	entries, err := covered.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("list %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		// A bind needs a target of the same kind, and sysfs directories this
		// serves hold only directories; anything else would vanish from view.
		if !e.IsDir() {
			return fmt.Errorf("%s is not a directory and would be hidden", filepath.Join(dir, e.Name()))
		}
		placeholder := filepath.Join(staged, e.Name())
		if err := os.MkdirAll(placeholder, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", placeholder, err)
		}
		names = append(names, e.Name())
	}

	if err := unix.Mount(staged, dir, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s over %s: %w", staged, dir, err)
	}

	coveredPath := fmt.Sprintf("/proc/self/fd/%d", covered.Fd())
	for _, n := range names {
		if err := unix.Mount(filepath.Join(coveredPath, n), filepath.Join(dir, n), "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			return errors.Join(fmt.Errorf("rebind %s: %w", filepath.Join(dir, n), err), detach(dir))
		}
	}

	return nil
}

// detach lazily unmounts dir together with every mount beneath it.
func detach(dir string) error {
	return unix.Unmount(dir, unix.MNT_DETACH)
}
