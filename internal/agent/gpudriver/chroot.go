// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// libSearchRoots are the loader's default roots, searched in order for a
// DT_NEEDED SONAME along with their multiarch subdirectories.
var libSearchRoots = []string{"/lib", "/usr/lib", "/lib64", "/usr/lib64"}

// chrootStageRoots are the driver-root-relative trees this file owns, for
// Discard to remove and as the hard boundary every staged destination must
// fall under.
//
// Deliberately not driver/usr/lib64: internal/agent/cdi bind-mounts that
// directory into consumer containers, and a second C runtime ahead of the
// container's own on the search path hands a binary a libc that does not match
// its loader — the reason deployments/nvml-mock/build/bundle-ib-tools.sh
// excludes glibc from what it bundles. Under driver/lib* the closure is on the
// loader's default path inside a chroot of the driver root, and invisible to
// every injected container.
var chrootStageRoots = []string{"driver/lib", "driver/lib64"}

// errNoInterp is returned by elfInterp when the ELF has no PT_INTERP segment.
// Shared libraries normally lack one; that is not a defect.
var errNoInterp = errors.New("no PT_INTERP segment")

// stageChrootRuntime makes the driver root chroot-able, so a caller can run
// `chroot <driver-root> nvidia-smi`.
//
// NVIDIA's GPU reset script reaches nvidia-smi only that way, and NVSentinel's
// janitor hardcodes DRIVER_ROOT=/run/nvidia/driver for it. That works on real
// hardware because the driver container r-bind-mounts its entire container root
// at that path, making it a complete filesystem. The mock stages driver
// surfaces alone, so the exec fails before nvidia-smi ever starts:
// `chroot: failed to run command 'nvidia-smi': No such file or directory`.
// Staging the loader and its libraries is what closes that gap.
// See issue #759.
func stageChrootRuntime(ctx context.Context, h *host.Host, _ *agent.State) error {
	sources := make([]string, 0, 2)
	if _, err := os.Stat(nvidiaSMISource); err == nil {
		sources = append(sources, nvidiaSMISource)
	}
	if matches, _ := filepath.Glob(nvmlShimGlob); len(matches) > 0 {
		sources = append(sources, matches[0])
	}
	if len(sources) == 0 {
		// Nothing dynamically linked to support. stageNvidiaSMI falls back to a
		// shell script here, and making that chroot-able would mean staging a
		// shell and its own closure too.
		return nil
	}

	closure, interps, err := chrootRuntimeClosure(sources, libSearchRoots)
	if err != nil {
		return err
	}

	driverRoot := filepath.Join(h.Root, "driver")
	for _, src := range closure {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Libraries remap a leading /usr so a RHEL-style /usr/lib64/libc.so.6
		// lands under driver/lib64, not the CDI-injected driver/usr/lib64. The
		// interpreter is exempt: the kernel looks up PT_INTERP as a literal
		// path inside the new root. fsutil.Copy reads through symlinks, which
		// is what the interpreter needs: preserved as a link, its target sits
		// outside the root and the exec fails with the very ENOENT this
		// function exists to fix.
		dst, err := chrootDest(driverRoot, src, !interps[src])
		if err != nil {
			return err
		}
		if err := fsutil.Copy(src, dst, 0o755); err != nil {
			return fmt.Errorf("stage %s: %w", src, err)
		}
	}
	return nil
}

// chrootDest maps a host absolute path into the driver root. When remapUSR is
// set, a leading /usr is stripped so libraries resolved under /usr/lib* land
// under driver/lib* rather than the CDI-injected driver/usr/lib64. The
// destination must fall under one of chrootStageRoots; anything else is a
// staging bug and fails loudly.
func chrootDest(driverRoot, src string, remapUSR bool) (string, error) {
	if !filepath.IsAbs(src) {
		return "", fmt.Errorf("chroot dest: %s is not an absolute path", src)
	}
	rel := strings.TrimPrefix(src, "/")
	if remapUSR {
		rel = strings.TrimPrefix(rel, "usr/")
	}
	dst := filepath.Join(driverRoot, rel)

	relInDriver, err := filepath.Rel(driverRoot, dst)
	if err != nil {
		return "", fmt.Errorf("chroot dest for %s: %w", src, err)
	}
	for _, root := range chrootStageRoots {
		stageRel := strings.TrimPrefix(root, "driver/")
		if relInDriver == stageRel || strings.HasPrefix(relInDriver, stageRel+string(filepath.Separator)) {
			return dst, nil
		}
	}
	return "", fmt.Errorf("chroot dest for %s lands at %s, outside %s",
		src, dst, strings.Join(chrootStageRoots, ", "))
}

// chrootRuntimeClosure returns the absolute host paths that must exist inside
// the driver root for a chrooted exec of binaries to work: each one's PT_INTERP
// loader plus the full transitive DT_NEEDED closure resolved against roots.
// Deduplicated, in discovery order. interps marks paths that came from
// PT_INTERP so staging can exempt them from /usr remapping.
func chrootRuntimeClosure(binaries []string, roots []string) ([]string, map[string]bool, error) {
	seen := make(map[string]bool)
	outSeen := make(map[string]bool)
	out := make([]string, 0, len(binaries)*8)
	interps := make(map[string]bool)
	queue := make([]string, 0, len(binaries)*4)

	enqueue := func(p string) {
		if !seen[p] {
			seen[p] = true
			queue = append(queue, p)
		}
	}
	addOut := func(p string) {
		if !outSeen[p] {
			outSeen[p] = true
			out = append(out, p)
		}
	}

	for _, bin := range binaries {
		enqueue(bin)
	}

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		interp, libs, err := elfDynamicDeps(path)
		if err != nil {
			return nil, nil, err
		}
		if interp != "" {
			addOut(interp)
			interps[interp] = true
		}
		for _, soname := range libs {
			p, err := resolveSoname(soname, roots)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, err)
			}
			addOut(p)
			enqueue(p)
		}
	}
	return out, interps, nil
}

// elfDynamicDeps returns the PT_INTERP path (empty when absent) and DT_NEEDED
// SONAMEs of path.
func elfDynamicDeps(path string) (interp string, libs []string, err error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("open %s: %w", path, err)
	}
	interp, interpErr := elfInterp(f)
	libs, libsErr := f.ImportedLibraries()
	if cerr := f.Close(); cerr != nil {
		return "", nil, fmt.Errorf("close %s: %w", path, cerr)
	}

	switch {
	case interpErr == nil:
		// keep interp
	case errors.Is(interpErr, errNoInterp):
		// Shared libraries carry no PT_INTERP; only the executable names the loader.
		interp = ""
	default:
		return "", nil, fmt.Errorf("PT_INTERP of %s: %w", path, interpErr)
	}
	if libsErr != nil {
		return "", nil, fmt.Errorf("read DT_NEEDED of %s: %w", path, libsErr)
	}
	return interp, libs, nil
}

// elfInterp returns the ELF's PT_INTERP path, the dynamic loader the kernel
// execs first. Read out of the binary rather than assumed, because the path is
// arch-dependent in a way a lookup table gets wrong: /lib/ld-linux-aarch64.so.1
// on arm64 against /lib64/ld-linux-x86-64.so.2 on amd64.
func elfInterp(f *elf.File) (string, error) {
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		raw, err := io.ReadAll(p.Open())
		if err != nil {
			return "", fmt.Errorf("read PT_INTERP: %w", err)
		}
		return string(bytes.TrimRight(raw, "\x00")), nil
	}
	return "", errNoInterp
}

// resolveSoname finds an absolute path for a DT_NEEDED entry, checking each
// root and then its immediate subdirectories. Scanning for the multiarch
// directory beats deriving its name from GOARCH: the triplet is a property of
// the image the closure is read from, not of the build, so a table would need
// an edit for every platform the mock is built for.
func resolveSoname(soname string, roots []string) (string, error) {
	for _, root := range roots {
		if candidate := filepath.Join(root, soname); exists(candidate) {
			return candidate, nil
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if candidate := filepath.Join(root, e.Name(), soname); exists(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("shared library %s not found under %s", soname, strings.Join(roots, ", "))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
