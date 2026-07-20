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
	// -O2 + _FORTIFY_SOURCE are required for the fortified rewrite to happen.
	bin := compileCHelper(t, cc, `#include <fcntl.h>
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
`, "-O2", "-D_FORTIFY_SOURCE=2")

	cmd := exec.Command(bin, "0", "/sys/bus/pci/devices/0000:07:00.0/config") // flags 0 == O_RDONLY
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "fortified open failed (config not redirected): %s", out)
	require.Equal(t, "222\n", string(out), "config[0] should be 0xde (222) from the mock tree")
}

// TestFopenPCIRedirect guards the fopen hook. libpci reads a device's
// `resource` file via fopen() (used by `lspci -v`), and glibc's fopen goes
// straight to an internal, non-interposable open — so without a dedicated
// fopen interposer the read escapes redirection and hits the real host path.
// Go never calls fopen, so this needs a small C consumer.
func TestFopenPCIRedirect(t *testing.T) {
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
	// device_id 0x233010DE -> vendor 0x10de, so config[0] == 0xde (222).
	ids := map[string]config.PCI{
		"0000:07:00.0": {BusID: "0000:07:00.0", DeviceID: 0x233010DE},
	}
	require.NoError(t, render.Render(render.Options{Topology: topo, Identities: ids, Output: root}))

	bin := compileCHelper(t, cc, `#include <stdio.h>
int main(int argc, char **argv) {
    FILE *f = fopen(argv[1], "rb");
    if (!f) { perror("fopen"); return 1; }
    int c = fgetc(f);
    if (c == EOF) return 2;
    printf("%d\n", c);
    return 0;
}
`, "-O2")

	cmd := exec.Command(bin, "/sys/bus/pci/devices/0000:07:00.0/config")
	cmd.Env = append(os.Environ(),
		"LD_PRELOAD="+shim,
		"MOCK_PCI_ROOT="+root,
	)
	out, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "fopen failed (config not redirected): %s", out)
	require.Equal(t, "222\n", string(out), "config[0] should be 0xde (222) via the fopen hook")
}

// compileCHelper compiles a small C program with the given extra cc flags and
// returns the built binary path. It centralizes the cc invocation shared by
// the shim's runtime-behavior tests, which need a real C consumer to exercise
// libc symbols (fortified open, fopen) that Go never calls directly.
func compileCHelper(t *testing.T, cc, src string, extraFlags ...string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "helper.c")
	require.NoError(t, os.WriteFile(srcPath, []byte(src), 0o644))
	bin := filepath.Join(dir, "helper")
	args := append([]string{}, extraFlags...)
	args = append(args, "-o", bin, srcPath)
	out, err := exec.Command(cc, args...).CombinedOutput()
	require.NoError(t, err, "compile helper: %s", out)
	return bin
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
