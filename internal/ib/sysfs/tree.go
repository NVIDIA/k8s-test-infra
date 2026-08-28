// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// staleDirPrefix names the scratch directory pruning renames doomed entries
// into. It lives inside the root so a rename never crosses a filesystem, and
// outside sys/ and dev/ so nothing walking the tree descends into it.
const staleDirPrefix = ".stale-"

// staleEntry is one path the current pass did not write.
type staleEntry struct {
	rel   string
	isDir bool
}

// tree records the paths a render pass wrote, so a later pass can drop what an
// earlier shape left behind.
//
// Workload processes read this tree through libibmocksys.so at arbitrary
// moments and cannot be paused, so no mutation may be observable half-done: an
// unchanged file is left alone, a changed one is swapped in by rename, and a
// departing directory is renamed away whole rather than emptied file by file.
type tree struct {
	root string
	kept map[string]struct{}
}

func newTree(root string) *tree {
	return &tree{root: root, kept: make(map[string]struct{})}
}

// keep records rel and every directory above it, so pruning never mistakes a
// surviving parent for garbage.
func (t *tree) keep(rel string) {
	for p := filepath.Clean(rel); p != "." && p != string(filepath.Separator); p = filepath.Dir(p) {
		t.kept[p] = struct{}{}
	}
}

func (t *tree) mkdir(rel string) error {
	if err := os.MkdirAll(filepath.Join(t.root, rel), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", rel, err)
	}

	t.keep(rel)

	return nil
}

// write brings rel to contents, leaving the file untouched when it already
// matches: a reconcile that changes nothing must disturb no reader.
func (t *tree) write(rel, contents string) error {
	full := filepath.Join(t.root, rel)

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
	}

	t.keep(rel)

	if current, err := os.ReadFile(full); err == nil && string(current) == contents {
		return nil
	}

	if err := writeAtomic(full, contents); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}

	return nil
}

// prune removes everything under the root that this pass did not write, which
// is how a shape that drops HCAs takes their directories with it. A pass that
// wrote nothing retracts the whole tree, leaving the root itself in place.
func (t *tree) prune() error {
	doomed, err := t.scanStale(".")

	if err != nil || len(doomed) == 0 {
		return err
	}

	// Created after the scan so it is never a candidate for its own pruning.
	staging, err := os.MkdirTemp(t.root, staleDirPrefix+"*")
	if err != nil {
		return fmt.Errorf("prune staging dir: %w", err)
	}

	errs := make([]error, 0, len(doomed)+1)

	for _, e := range doomed {
		errs = append(errs, t.retract(e, staging))
	}

	if err := os.RemoveAll(staging); err != nil {
		errs = append(errs, fmt.Errorf("remove %s: %w", staging, err))
	}

	return errors.Join(errs...)
}

// scanStale walks rel and returns what this pass did not write. A stale
// directory is returned whole and never descended into: it leaves in one piece.
func (t *tree) scanStale(rel string) ([]staleEntry, error) {
	entries, err := os.ReadDir(filepath.Join(t.root, rel))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}

	var out []staleEntry
	for _, e := range entries {
		child := filepath.Join(rel, e.Name())
		if _, ok := t.kept[child]; !ok {
			out = append(out, staleEntry{rel: child, isDir: e.IsDir()})
			continue
		}
		if !e.IsDir() {
			continue
		}
		nested, err := t.scanStale(child)
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

// retract unhooks one stale path. A directory goes in a single rename, so a
// consumer meets either a whole HCA or none of it; freeing the bytes afterwards
// races nobody, because nothing can reach them by path any more.
func (t *tree) retract(e staleEntry, staging string) error {
	full := filepath.Join(t.root, e.rel)
	if !e.isDir {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", e.rel, err)
		}
		return nil
	}

	dest := filepath.Join(staging, strings.ReplaceAll(e.rel, string(filepath.Separator), "_"))
	if err := os.Rename(full, dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("retract %s: %w", e.rel, err)
	}
	return nil
}

// writeAtomic replaces full in a single rename, so a reader sees either the
// old contents or the new ones and never a truncated attribute.
func writeAtomic(full, contents string) error {
	f, err := os.CreateTemp(filepath.Dir(full), "."+filepath.Base(full)+".tmp*")

	if err != nil {
		return err
	}

	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once the rename lands

	if _, err := f.WriteString(contents); err != nil {
		_ = f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	// CreateTemp opens 0600; sysfs attributes are world-readable.
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, full)
}
