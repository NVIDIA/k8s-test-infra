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
	Modules []Module

	// SourceRoot is the host /sys/module to mirror. Empty or absent serves the
	// simulated modules alone.
	SourceRoot string

	// OverlayRoot is where the tree is staged. Render writes to
	// <OverlayRoot>/sys/module and errors when it is empty.
	OverlayRoot string

	// Host is the node's parsed /proc/modules. Render and ProcModules both read
	// host presence from it, so the two surfaces agree.
	Host HostModules
}

// Render stages the module tree and returns the source paths it could not read.
// It is idempotent and converging: a module the source no longer lists is pruned.
func Render(o Options) ([]string, error) {
	if o.OverlayRoot == "" {
		return nil, errors.New("kmod render: OverlayRoot is required")
	}

	root := filepath.Join(o.OverlayRoot, SysModuleRelPath)
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

	for _, m := range o.Modules {
		if err := serveModule(root, m, declared[m.Name], o.Host.byName); err != nil {
			return skipped, err
		}
		// serveModule always writes under root/<name>, so keep it from the prune.
		declared[m.Name] = true
	}

	return skipped, fsutil.PruneDir(root, func(name string) bool { return declared[name] })
}

// Clear empties the staged module tree.
func Clear(root string) error {
	return fsutil.PruneDir(filepath.Join(root, SysModuleRelPath), func(string) bool { return false })
}

// serveModule writes one simulated module's directory.
//
// A module the host loads takes its values from the host's /proc/modules line,
// because ProcModules serves that same line and the two surfaces must agree.
//
// A mirrored directory holds what this pass copied from the node, so it needs
// only its gaps filled. An unmirrored one can still hold an earlier pass's
// values, of either kind, so it gets every attribute written and the rest pruned.
func serveModule(root string, m Module, mirrored bool, loaded map[string]hostModule) error {
	dir := filepath.Join(root, m.Name)
	host, isHost := loaded[m.Name]

	if mirrored {
		if isHost {
			if err := fillHostGaps(dir, host); err != nil {
				return fmt.Errorf("kmod render: fill %s: %w", m.Name, err)
			}
			return nil
		}
		if err := writeAttrs(dir, moduleAttrs(m), m.Holders); err != nil {
			return fmt.Errorf("kmod render: render %s: %w", m.Name, err)
		}
		return nil
	}

	// Nothing else creates holders/ for a module the mirror did not copy.
	if err := os.MkdirAll(filepath.Join(dir, "holders"), 0o755); err != nil {
		return fmt.Errorf("kmod render: mkdir %s: %w", dir, err)
	}

	attrs, holders := moduleAttrs(m), m.Holders
	if isHost {
		// Nothing mirrored holders/ here, so the line is the only evidence.
		attrs, holders = hostAttrs(host), host.holders
	}
	if err := writeAttrs(dir, attrs, holders); err != nil {
		return fmt.Errorf("kmod render: render %s: %w", m.Name, err)
	}

	return fsutil.PruneDir(dir, func(name string) bool {
		_, isAttribute := attrs[name]
		return name == "holders" || isAttribute
	})
}

// fillHostGaps writes what the mirror failed to copy, from the host's
// /proc/modules line. It reconciles holders/ instead of filling it: the mirror
// can keep that directory and still lose a link inside it. It reconciles only
// when the line carried a dependency field, because absent is not none.
func fillHostGaps(dir string, host hostModule) error {
	for attr, content := range hostAttrs(host) {
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

// hostAttrs are what a host /proc/modules line supplies. It carries no version,
// so a host module gets none rather than the simulated driver's.
func hostAttrs(host hostModule) map[string]string {
	return map[string]string{
		"coresize":  host.sizeBytes + "\n",
		"refcnt":    host.refcnt + "\n",
		"initstate": initStateLive + "\n",
	}
}

func moduleAttrs(m Module) map[string]string {
	attrs := map[string]string{
		"refcnt":    strconv.Itoa(m.Refcnt()) + "\n",
		"coresize":  strconv.Itoa(m.SizeBytes) + "\n",
		"initstate": initStateLive + "\n",
	}
	if m.Version != "" {
		attrs["version"] = m.Version + "\n"
	}
	return attrs
}

func writeAttrs(dir string, attrs map[string]string, holders []string) error {
	for name, content := range attrs {
		if err := fsutil.Write(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}

	return linkHolders(dir, holders)
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

// mirrorTree copies src onto dst, and reports success plus the paths it could
// not read. A read failure skips the entry, so an unreadable host module
// degrades the mirror instead of failing the stage.
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
