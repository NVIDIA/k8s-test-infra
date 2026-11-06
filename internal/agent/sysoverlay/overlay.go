// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package sysoverlay adds an entry to a sysfs directory the kernel did not
// create it in. sysfs refuses mkdir, so the entry is staged in a tree of our
// own that is mounted over the directory, and each of the directory's own
// entries is bound back into that tree so nothing the kernel shows is lost.
//
// It serves surfaces a kernel omits for want of firmware or configuration —
// DMI without SMBIOS, NUMA without CONFIG_NUMA — and never touches an entry
// the kernel provides.
package sysoverlay

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// View is what a directory shows at an overlay's entry.
type View int

const (
	// Absent means the kernel provides no entry, so it is ours to serve.
	Absent View = iota
	// Kernel means the kernel provides the entry. It is never touched.
	Kernel
	// Served means the entry is the staged one, through our overmount.
	Served
)

// Overlay adds Entry to Dir.
type Overlay struct {
	// Dir is the existing sysfs directory, as this process sees it.
	Dir string
	// Staged is the tree mounted over Dir. It holds Entry; Serve adds a
	// placeholder for each of Dir's own entries.
	Staged string
	// Entry is the name of the child of Dir the overlay adds.
	Entry string
}

// At returns the overlay adding the sysfs entry at relPath, relative to /sys,
// staged under h's staging area in a directory named after its parent.
func At(h *host.Host, relPath string) Overlay {
	parent, entry := filepath.Split(filepath.Clean(relPath))
	parent = filepath.Clean(parent)
	return Overlay{
		Dir:    h.SysPath(parent),
		Staged: h.RootPath("sys-" + strings.ReplaceAll(parent, "/", "-")),
		Entry:  entry,
	}
}

// StagedEntry is where the entry is written before and while it is served.
func (o Overlay) StagedEntry() string { return filepath.Join(o.Staged, o.Entry) }

// Look classifies Dir's entry. Through a bind mount the served entry is the
// staged one, so identity of the two tells our overmount from the kernel's own
// entry — including an overmount left behind by a process that exited without
// withdrawing it.
func (o Overlay) Look() (View, error) {
	shownPath := filepath.Join(o.Dir, o.Entry)
	shown, err := os.Stat(shownPath)
	if errors.Is(err, fs.ErrNotExist) {
		return Absent, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", shownPath, err)
	}

	staged, err := os.Stat(o.StagedEntry())
	if errors.Is(err, fs.ErrNotExist) {
		return Kernel, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", o.StagedEntry(), err)
	}

	if os.SameFile(shown, staged) {
		return Served, nil
	}
	return Kernel, nil
}

// Serve mounts the staged tree over Dir. A no-op once served; an error where
// the kernel provides the entry, which an overmount would hide.
func (o Overlay) Serve() error {
	v, err := o.Look()
	if err != nil {
		return err
	}
	switch v {
	case Served:
		return nil
	case Kernel:
		return fmt.Errorf("kernel provides %s, not serving over it", filepath.Join(o.Dir, o.Entry))
	case Absent:
		if err := overmount(o.Staged, o.Dir); err != nil {
			return fmt.Errorf("serve %s over %s: %w", o.Staged, o.Dir, err)
		}
		return nil
	}
	return fmt.Errorf("unknown view %d of %s", v, filepath.Join(o.Dir, o.Entry))
}

// Withdraw detaches the overmount, returning Dir to the kernel's own view. A
// no-op unless served, so it never detaches a mount it did not make.
func (o Overlay) Withdraw() error {
	v, err := o.Look()
	if err != nil {
		return err
	}
	if v != Served {
		return nil
	}

	if err := detach(o.Dir); err != nil {
		return fmt.Errorf("unmount %s: %w", o.Dir, err)
	}
	return nil
}
