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

// TestFortifiedOpenPCIRedirect guards the _FORTIFY_SOURCE open path. When a
// caller opens a file with a non-constant flags argument (as libpci does for a
// PCI `config` file), glibc's fortify headers emit __open_2 instead of open.
// If the shim only interposes open/openat, that call escapes redirection and
// hits the real host path — the exact bug behind lspci's "Cannot open
// .../config" warning. This compiles a tiny helper that forces __open_2 and
// asserts it reads the redirected mock config.
func TestFortifiedOpenPCIRedirect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires linux")
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	shim := filepath.Join(wd, "..", "libpcimocksys.so")
	if _, statErr := os.Stat(shim); statErr != nil {
		t.Skipf("shim not built: %v (run make -C pkg/system/mockpcisysfs)", statErr)
	}
	cc, lookErr := exec.LookPath("cc")
	if lookErr != nil {
		t.Skipf("cc not available: %v", lookErr)
	}

	root := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	// device_id 0x233010DE -> vendor 0x10de, so config[0] == 0xde.
	ids := map[string]config.PCI{
		"0000:07:00.0": {BusID: "0000:07:00.0", DeviceID: 0x233010DE},
	}
	require.NoError(t, render.Render(render.Options{Topology: topo, Identities: ids, Output: root}))

	// Helper: open(argv[2], flags=atoi(argv[1])) — the runtime flags value
	// forces the compiler to emit __open_2 under _FORTIFY_SOURCE. It prints
	// the first byte of config as a decimal so the test can assert 0xde.
	src := filepath.Join(t.TempDir(), "fortopen.c")
	const prog = `#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>
int main(int argc, char **argv) {
    int flags = atoi(argv[1]);
    int fd = open(argv[2], flags);
    if (fd < 0) { perror("open"); return 1; }
    unsigned char b = 0;
    if (read(fd, &b, 1) != 1) return 2;
    printf("%d\n", b);
    return 0;
}
`
	require.NoError(t, os.WriteFile(src, []byte(prog), 0o644))
	bin := filepath.Join(t.TempDir(), "fortopen")
	// -O2 is required for _FORTIFY_SOURCE to take effect.
	build := exec.Command(cc, "-O2", "-D_FORTIFY_SOURCE=2", "-o", bin, src)
	buildOut, buildErr := build.CombinedOutput()
	require.NoError(t, buildErr, "compile helper: %s", buildOut)

	cmd := exec.Command(bin, "0", "/sys/bus/pci/devices/0000:07:00.0/config") // flags 0 == O_RDONLY
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "fortified open failed (config not redirected): %s", out)
	require.Equal(t, "222\n", string(out), "config[0] should be 0xde (222) from the mock tree")
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
