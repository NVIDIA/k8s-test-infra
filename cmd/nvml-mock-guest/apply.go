// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/guestdisk"
)

// applier copies the host's overrides from the disk's live area into the
// guest's override file, which the mock library re-reads on its own.
type applier struct {
	disk      string
	overrides string

	last    [32]byte
	applied bool
}

// apply reports whether it changed the guest's overrides. The host is the
// source of truth: a guest-side edit lasts until the host's next change.
func (a *applier) apply() (bool, error) {
	area, err := guestdisk.Read(a.disk)
	if err != nil {
		return false, err
	}
	payload, sum, err := guestdisk.Decode(area)
	if err != nil {
		return false, err
	}
	if !a.applied {
		// A restarted program starts with what an earlier run applied.
		current, err := a.current()
		if err != nil {
			return false, err
		}
		a.last, a.applied = sha256.Sum256(current), true
	}
	if sum == a.last {
		return false, nil
	}
	if err := a.write(payload); err != nil {
		return false, err
	}
	a.last = sum

	return true, nil
}

// write replaces the guest's override file; an empty payload removes it.
func (a *applier) write(payload []byte) error {
	unlock, err := fsutil.LockSibling(a.overrides)
	if err != nil {
		return fmt.Errorf("lock %s: %w", a.overrides, err)
	}
	defer unlock()
	if len(payload) > 0 {
		return fsutil.Write(a.overrides, payload, 0o644)
	}
	if err := os.Remove(a.overrides); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// current is the guest's override file; no file is no overrides.
func (a *applier) current() ([]byte, error) {
	data, err := os.ReadFile(a.overrides)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	return data, err
}
