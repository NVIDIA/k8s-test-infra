// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Integration test that renders a fake PCI sysfs tree and runs readlink(1)
// against it via libpcimocksys.so. Skipped unless the integration tag is set
// AND we are on a Linux host with the prebuilt shim available.
//
// Run with:
//
//	make -C pkg/system/mockpcisysfs
//	go test -tags=integration ./pkg/system/mockpcisysfs/shim/... -v
package shim

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

func TestReadlink_Integration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("integration test requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err, "getwd")
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, err := os.Stat(shim); err != nil {
		t.Skipf("shim not built: %v (run `make -C pkg/system/mockpcisysfs`)", err)
	}

	root := t.TempDir()
	err = render.Render(render.Options{
		Topology: &config.PCIeTopology{
			RootComplexes: []config.RootComplex{{
				ID:       "pci0000:00",
				NUMANode: 0,
				Devices:  []string{"0000:07:00.0"},
			}},
		},
		Output: root,
	})
	require.NoError(t, err, "Render")

	cmd := exec.Command("readlink", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "readlink failed\noutput:\n%s", out)
	require.Contains(t, string(out), "pci0000:00/0000:07:00.0",
		"readlink output missing expected root-complex path\nfull output:\n%s", out)
}
