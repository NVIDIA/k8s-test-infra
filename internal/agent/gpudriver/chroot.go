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
// Discard to remove.
//
// Deliberately not driver/usr/lib64: internal/agent/cdi bind-mounts that
// directory into consumer containers, and a second C runtime ahead of the
// container's own on the search path hands a binary a libc that does not match
// its loader — the reason deployments/nvml-mock/build/bundle-ib-tools.sh
// excludes glibc from what it bundles. Under driver/lib* the closure is on the
// loader's default path inside a chroot of the driver root, and invisible to
// every injected container.
var chrootStageRoots = []string{"driver/lib", "driver/lib64"}

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

	closure, err := chrootRuntimeClosure(sources, libSearchRoots)
	if err != nil {
		return err
	}

	for _, src := range closure {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Staged at the same absolute path inside the driver root, so the
		// loader inside the chroot finds each file on its own default search
		// path with no RPATH or ld.so.cache involved. fsutil.Copy reads through
		// symlinks, which is what the interpreter needs: preserved as a link,
		// its target sits outside the root and the exec fails with the very
		// ENOENT this function exists to fix.
		dst := filepath.Join(h.Root, "driver", strings.TrimPrefix(src, "/"))
		if err := fsutil.Copy(src, dst, 0o755); err != nil {
			return fmt.Errorf("stage %s: %w", src, err)
		}
	}
	return nil
}

// chrootRuntimeClosure returns the absolute host paths that must exist inside
// the driver root for a chrooted exec of binaries to work: each one's PT_INTERP
// loader plus every DT_NEEDED library resolved against roots. Deduplicated,
// in discovery order.
func chrootRuntimeClosure(binaries []string, roots []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(binaries)*4)
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, bin := range binaries {
		f, err := elf.Open(bin)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", bin, err)
		}
		interp, interpErr := elfInterp(f)
		libs, libsErr := f.ImportedLibraries()
		_ = f.Close()

		// A shared library carries no PT_INTERP, and that is not a defect: only
		// the executable names the loader. So the error is deliberately not
		// propagated, unlike a DT_NEEDED that cannot be read.
		if interpErr == nil {
			add(interp)
		}
		if libsErr != nil {
			return nil, fmt.Errorf("read DT_NEEDED of %s: %w", bin, libsErr)
		}
		for _, soname := range libs {
			p, err := resolveSoname(soname, roots)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", bin, err)
			}
			add(p)
		}
	}
	return out, nil
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
	return "", errors.New("no PT_INTERP segment")
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
