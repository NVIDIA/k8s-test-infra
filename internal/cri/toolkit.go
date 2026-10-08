// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const (
	// toolkitDir holds Mokka's copy of the toolkit on the host. It is outside
	// /var/lib/nvml-mock, which the NRI plugin mounts into containers.
	toolkitDir = "/usr/local/nvml-mock/toolkit"
	// runtimeConfigPath is where the NVIDIA runtime reads its configuration.
	runtimeConfigPath = "/etc/nvidia-container-runtime/config.toml"

	// DefaultToolkitSource is where the image ships the toolkit binaries.
	DefaultToolkitSource = "/usr/local/libexec/nvml-mock/container-toolkit"
)

// toolkitBinaries are the NVIDIA Container Toolkit binaries Mokka installs.
var toolkitBinaries = []string{"nvidia-container-runtime", "nvidia-ctk", "nvidia-cdi-hook"}

// runtimeConfig is the NVIDIA runtime's configuration in CDI mode.
//
//go:embed nvidia-container-runtime.toml
var runtimeConfig []byte

// packagePaths maps each path a package install owns to the file in the
// toolkit directory that Mokka links there. Consumers expect the hook at
// /usr/bin/nvidia-cdi-hook, as the GPU Operator's toolkit.enabled=false does.
func packagePaths() map[string]string {
	paths := map[string]string{runtimeConfigPath: toolkitDir + "/config.toml"}

	for _, b := range toolkitBinaries {
		paths["/usr/bin/"+b] = toolkitDir + "/" + b
	}

	return paths
}

// ForeignToolkit lists the package paths that hold something Mokka did not
// put there: another installer owns the toolkit on this node.
func ForeignToolkit(h *host.Host) ([]string, error) {
	var foreign []string
	for path, target := range packagePaths() {
		info, err := os.Lstat(h.HostPath(path))
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", path, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			if dest, err := os.Readlink(h.HostPath(path)); err == nil && dest == target {
				continue
			}
		}

		foreign = append(foreign, path)
	}

	slices.Sort(foreign)

	return foreign, nil
}

// InstallToolkit copies the binaries from source and the runtime
// configuration into the toolkit directory, and links them where a package
// install puts them. A binary is replaced only when it changed: containers
// created through the handler run it. Links are only ever created, never
// replaced, so the paths never go missing; callers check ForeignToolkit
// first, so the existing ones are Mokka's.
func InstallToolkit(h *host.Host, source string) error {
	if err := installBinaries(h, source); err != nil {
		return err
	}

	config := h.HostPath(toolkitDir + "/config.toml")
	if current, err := os.ReadFile(config); err != nil || !bytes.Equal(current, runtimeConfig) {
		if err := fsutil.Write(config, runtimeConfig, 0o644); err != nil {
			return err
		}
	}

	return linkPackagePaths(h)
}

func installBinaries(h *host.Host, source string) error {
	for _, b := range toolkitBinaries {
		src := filepath.Join(source, b)
		dst := h.HostPath(toolkitDir + "/" + b)

		same, err := sameContents(src, dst)

		if err != nil {
			return err
		}

		if !same {
			if err := fsutil.Copy(src, dst, 0o755); err != nil {
				return err
			}
		}
	}

	return nil
}

func linkPackagePaths(h *host.Host) error {
	for path, target := range packagePaths() {
		link := h.HostPath(path)
		if _, err := os.Lstat(link); err == nil {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}

		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link %s to %s: %w", path, target, err)
		}
	}

	return nil
}

// sameContents reports whether dst holds what src does. src must exist; a
// missing dst differs.
func sameContents(src, dst string) (bool, error) {
	want, err := fileHash(src)
	if err != nil {
		return false, fmt.Errorf("the image has no %s: %w", filepath.Base(src), err)
	}

	got, err := fileHash(dst)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return bytes.Equal(want, got), nil
}

func fileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return sum.Sum(nil), nil
}
