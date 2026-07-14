//go:build integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package shim_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
	"github.com/stretchr/testify/require"
)

func TestReadlinkPCIRedirect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, statErr := os.Stat(shim); statErr != nil {
		t.Skipf("shim not built: %v (run make -C pkg/system/mockpcisysfs)", statErr)
	}

	root := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID:       "pci0000:00",
			NUMANode: 0,
			Devices:  []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, render.Render(render.Options{Topology: topo, Output: root}))

	cmd := exec.Command("readlink", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "readlink failed: %s", out)
	require.Contains(t, string(out), "pci0000:00/0000:07:00.0")
}

func TestOpenSysDevicesPCIRedirect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, statErr := os.Stat(shim); statErr != nil {
		t.Skipf("shim not built: %v (run make -C pkg/system/mockpcisysfs)", statErr)
	}

	root := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID:       "pci0000:00",
			NUMANode: 0,
			Devices:  []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, render.Render(render.Options{Topology: topo, Output: root}))

	cmd := exec.Command("cat", "/sys/devices/pci0000:00/0000:07:00.0/numa_node")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "cat failed: %s", out)
	require.Equal(t, "0\n", string(out))
}

// TestRewriteOverflowFailsClosed asserts that when MOCK_PCI_ROOT is so long
// the rewritten path would overflow the buffer, the shim fails the call with
// ENAMETOOLONG instead of silently falling back to the real host path.
func TestRewriteOverflowFailsClosed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, statErr := os.Stat(shim); statErr != nil {
		t.Skipf("shim not built: %v (run make -C pkg/system/mockpcisysfs)", statErr)
	}

	// A ~4090-byte root guarantees root_len + len(matched path) + 1 exceeds the
	// shim's PATH_MAX (4096) buffer, forcing the overflow branch.
	longRoot := "/" + strings.Repeat("a", 4090)
	cmd := exec.Command("readlink", "-v", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+longRoot,
	)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "expected readlink to fail with ENAMETOOLONG, got: %s", out)
	require.Contains(t, strings.ToLower(string(out)), "too long",
		"expected an ENAMETOOLONG error, got: %s", out)
}
