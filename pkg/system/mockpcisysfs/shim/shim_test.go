//go:build integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package shim_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
