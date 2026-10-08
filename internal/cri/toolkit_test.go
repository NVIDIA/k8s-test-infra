// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// toolkitSource is an image's toolkit directory with every binary in it.
func toolkitSource(t *testing.T) string {
	t.Helper()

	source := t.TempDir()
	for _, b := range toolkitBinaries {
		require.NoError(t, os.WriteFile(filepath.Join(source, b), []byte("#!/bin/sh\n# "+b+"\n"), 0o755))
	}

	return source
}

// link returns the target of the link at path on the node.
func link(t *testing.T, h *host.Host, path string) string {
	t.Helper()

	target, err := os.Readlink(h.HostPath(path))
	require.NoError(t, err)

	return target
}

func TestInstallToolkit(t *testing.T) {
	h := host.New(t.TempDir())

	require.NoError(t, InstallToolkit(h, toolkitSource(t)))

	for _, b := range toolkitBinaries {
		info, err := os.Stat(h.HostPath(toolkitDir + "/" + b))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
		require.Equal(t, toolkitDir+"/"+b, link(t, h, "/usr/bin/"+b), "linked where a package install puts it")
	}

	require.Equal(t, toolkitDir+"/config.toml", link(t, h, runtimeConfigPath))
	config, err := os.ReadFile(h.HostPath(toolkitDir + "/config.toml"))
	require.NoError(t, err)
	require.Contains(t, string(config), `mode = "cdi"`)
}

func TestInstallToolkitKeepsAnUnchangedBinary(t *testing.T) {
	h := host.New(t.TempDir())
	source := toolkitSource(t)
	require.NoError(t, InstallToolkit(h, source))
	before, err := os.Stat(h.HostPath(toolkitDir + "/nvidia-cdi-hook"))
	require.NoError(t, err)

	require.NoError(t, InstallToolkit(h, source))

	after, err := os.Stat(h.HostPath(toolkitDir + "/nvidia-cdi-hook"))
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "a binary containers may be running is replaced only when it changed")
}

func TestInstallToolkitWithoutABinary(t *testing.T) {
	h := host.New(t.TempDir())
	source := toolkitSource(t)
	require.NoError(t, os.Remove(filepath.Join(source, "nvidia-ctk")))

	require.ErrorContains(t, InstallToolkit(h, source), "nvidia-ctk", "the error names the missing binary")
}

func TestForeignToolkit(t *testing.T) {
	h := host.New(t.TempDir())

	foreign, err := ForeignToolkit(h)
	require.NoError(t, err)
	require.Empty(t, foreign, "nothing is installed yet")

	require.NoError(t, InstallToolkit(h, toolkitSource(t)))
	foreign, err = ForeignToolkit(h)
	require.NoError(t, err)
	require.Empty(t, foreign, "Mokka's own links are not foreign")

	require.NoError(t, os.Remove(h.HostPath("/usr/bin/nvidia-ctk")))
	require.NoError(t, os.WriteFile(h.HostPath("/usr/bin/nvidia-ctk"), []byte("apt"), 0o755))
	foreign, err = ForeignToolkit(h)
	require.NoError(t, err)
	require.Equal(t, []string{"/usr/bin/nvidia-ctk"}, foreign)
}
