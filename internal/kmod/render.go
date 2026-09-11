// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// Options configures Render.
type Options struct {
	// Modules are the simulated modules to serve alongside the host's own.
	Modules []Module

	// SourceRoot is the host /sys/module directory to mirror. An empty or
	// absent SourceRoot serves the simulated modules alone.
	SourceRoot string

	// Output is the overlay root. The renderer writes under
	// <Output>/sys/module and Render returns an error when it is empty.
	Output string

	// HostProcModules is the host /proc/modules text. Render and ProcModules
	// both take host presence from it, so the two surfaces agree on a module.
	HostProcModules string
}

// Render stages the module tree and returns the source paths it could not read.
// It is idempotent and converging: a module the source no longer lists is pruned.
func Render(o Options) ([]string, error) {
	if o.Output == "" {
		return nil, errors.New("kmod render: Output is required")
	}

	root := filepath.Join(o.Output, SysModuleRelPath)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("kmod render: mkdir %s: %w", root, err)
	}

	var source []os.DirEntry
	if o.SourceRoot != "" {
		entries, err := os.ReadDir(o.SourceRoot)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("kmod render: read %s: %w", o.SourceRoot, err)
		}
		source = entries
	}

	var skipped []string
	declared := make(map[string]bool, len(source)+len(o.Modules))
	for _, e := range source {
		name := e.Name()
		mirrored, missed, err := mirrorTree(filepath.Join(o.SourceRoot, name), filepath.Join(root, name))
		skipped = append(skipped, missed...)
		if err != nil {
			return skipped, fmt.Errorf("kmod render: mirror %s: %w", name, err)
		}
		declared[name] = mirrored
	}

	rendered, err := renderModules(root, o.Modules, declared, loadedModules(o.HostProcModules))
	if err != nil {
		return skipped, err
	}
	for _, name := range rendered {
		declared[name] = true
	}

	return skipped, fsutil.PruneDir(root, func(name string) bool { return declared[name] })
}

// Clear empties the staged module tree.
func Clear(root string) error {
	return fsutil.PruneDir(filepath.Join(root, SysModuleRelPath), func(string) bool { return false })
}

// renderModules serves every simulated module and returns the names it created
// outright, which the caller must keep from the prune.
func renderModules(root string, mods []Module, declared map[string]bool, loaded map[string]hostModule) ([]string, error) {
	var rendered []string

	for _, m := range mods {
		host, isHost := loaded[m.Name]

		switch {
		case declared[m.Name] && isHost:
			if err := fillHostGaps(root, m.Name, host); err != nil {
				return rendered, fmt.Errorf("kmod render: fill %s: %w", m.Name, err)
			}
		case declared[m.Name]:
			if err := completeModule(root, m); err != nil {
				return rendered, fmt.Errorf("kmod render: complete %s: %w", m.Name, err)
			}
		default:
			if err := renderModule(root, m); err != nil {
				return rendered, fmt.Errorf("kmod render: render %s: %w", m.Name, err)
			}
			rendered = append(rendered, m.Name)
		}
	}

	return rendered, nil
}

// fillHostGaps writes what the mirror failed to copy, from the host's own
// /proc/modules line. holders/ is reconciled rather than filled, because the
// mirror can keep the directory and still lose a link inside it, and only when
// the line carried a dependency field: absent is not the same as none. version
// is not in that line to recover, so it stays absent rather than carrying the
// simulated driver's version onto a host module.
func fillHostGaps(root, name string, host hostModule) error {
	dir := filepath.Join(root, name)

	for attr, content := range map[string]string{
		"coresize":  host.coreSize + "\n",
		"refcnt":    host.refcnt + "\n",
		"initstate": initStateLive + "\n",
	} {
		path := filepath.Join(dir, attr)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := fsutil.Write(path, []byte(content), 0o644); err != nil {
			return err
		}
	}

	if !host.holdersKnown {
		return nil
	}

	return linkHolders(dir, host.holders)
}

func moduleAttrs(m Module) map[string]string {
	attrs := map[string]string{
		"refcnt":    strconv.Itoa(m.Refcnt()) + "\n",
		"coresize":  strconv.Itoa(m.CoreSize) + "\n",
		"initstate": initStateLive + "\n",
	}
	if m.Version != "" {
		attrs["version"] = m.Version + "\n"
	}
	return attrs
}

func completeModule(root string, m Module) error {
	dir := filepath.Join(root, m.Name)

	for name, content := range moduleAttrs(m) {
		if err := fsutil.Write(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}

	return linkHolders(dir, m.Holders)
}

func renderModule(root string, m Module) error {
	dir := filepath.Join(root, m.Name)
	if err := os.MkdirAll(filepath.Join(dir, "holders"), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	attrs := moduleAttrs(m)

	for name, content := range attrs {
		if err := fsutil.Write(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}

	if err := linkHolders(dir, m.Holders); err != nil {
		return err
	}

	return fsutil.PruneDir(dir, func(name string) bool {
		_, isAttribute := attrs[name]
		return name == "holders" || isAttribute
	})
}

func linkHolders(dir string, holders []string) error {
	declared := make(map[string]bool, len(holders))
	for _, h := range holders {
		link := filepath.Join(dir, "holders", h)
		if err := fsutil.Symlink(filepath.Join("..", "..", h), link); err != nil {
			return err
		}
		declared[h] = true
	}

	return fsutil.PruneDir(filepath.Join(dir, "holders"), func(name string) bool { return declared[name] })
}

// mirrorTree copies src onto dst and reports whether it succeeded, along with
// the source paths it could not read. A read failure skips the entry, which the
// prune then removes, so an unreadable host module degrades the mirror rather
// than failing the whole stage.
func mirrorTree(src, dst string) (bool, []string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return false, []string{src}, nil
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return false, nil, fmt.Errorf("kmod render: mkdir %s: %w", dst, err)
	}

	var skipped []string
	declared := make(map[string]bool, len(entries))
	for _, e := range entries {
		mirrored, missed, err := mirrorEntry(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), e)
		skipped = append(skipped, missed...)
		if err != nil {
			return false, skipped, err
		}
		declared[e.Name()] = mirrored
	}

	return true, skipped, fsutil.PruneDir(dst, func(name string) bool { return declared[name] })
}

func mirrorEntry(src, dst string, e os.DirEntry) (bool, []string, error) {
	switch {
	case e.Type()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return false, []string{src}, nil
		}
		return true, nil, fsutil.Symlink(target, dst)
	case e.IsDir():
		return mirrorTree(src, dst)
	default:
		content, err := os.ReadFile(src)
		if err != nil {
			return false, []string{src}, nil
		}

		info, err := e.Info()
		if err != nil {
			return false, []string{src}, nil
		}

		return true, nil, fsutil.Write(dst, content, info.Mode().Perm())
	}
}
