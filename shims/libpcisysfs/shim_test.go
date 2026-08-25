//go:build integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package libpcisysfs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
	"github.com/stretchr/testify/require"
)

// requireLinux guards tests that need Linux LD_PRELOAD behaviour. The
// integration build tag prevents accidental execution on a macOS workstation,
// and this call skips at run time so the test shows up in results rather than
// silently disappearing.
func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("requires linux, running on %s", runtime.GOOS)
	}
}

// requireShim returns the path to the built libpcisysfs.so or skips the test
// if it hasn't been built yet.
func requireShim(t *testing.T) string {
	t.Helper()
	return requireBuilt(t, "libpcisysfs.so")
}

// requireTestBin returns the path to a Makefile-built C test binary or skips
// the test if it hasn't been built yet.
func requireTestBin(t *testing.T, name string) string {
	t.Helper()
	return requireBuilt(t, name)
}

func requireBuilt(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	path := filepath.Join(wd, name)
	if _, statErr := os.Stat(path); statErr != nil {
		t.Skipf("%s not built: %v (run make -C shims/libpcisysfs)", name, statErr)
	}
	return path
}

// renderBasicTree writes a single-device sysfs tree under root using the
// canonical test BDF (0000:07:00.0, root pci0000:00, NUMA 0).
func renderBasicTree(t *testing.T, root string) {
	t.Helper()
	topo := &pcisysfs.PCIeTopology{
		RootComplexes: []pcisysfs.RootComplex{{
			ID:       "pci0000:00",
			NUMANode: 0,
			Devices:  []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, pcisysfs.Render(pcisysfs.Options{Topology: topo, Output: root}))
}

func TestReadlinkPCIRedirect(t *testing.T) {
	requireLinux(t)
	shim := requireShim(t)

	root := t.TempDir()
	renderBasicTree(t, root)

	cmd := exec.Command("readlink", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+shim, "MOCK_PCI_ROOT="+root)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "readlink failed: %s", out)
	require.Contains(t, string(out), "pci0000:00/0000:07:00.0")
}

func TestOpenSysDevicesPCIRedirect(t *testing.T) {
	requireLinux(t)
	shim := requireShim(t)

	root := t.TempDir()
	renderBasicTree(t, root)

	cmd := exec.Command("cat", "/sys/devices/pci0000:00/0000:07:00.0/numa_node")
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+shim, "MOCK_PCI_ROOT="+root)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "cat failed: %s", out)
	require.Equal(t, "0\n", string(out))
}

// TestFortifiedOpenPCIRedirect guards the _FORTIFY_SOURCE open path. When a
// caller opens a file with a non-constant flags argument (as libpci does for a
// PCI `config` file), glibc's fortify headers emit __open_2 instead of open.
// If the shim only interposes open/openat, that call escapes redirection and
// hits the real host path — the exact bug behind lspci's "Cannot open
// .../config" warning. open-fortified is compiled with -D_FORTIFY_SOURCE=2
// to force the fortified path; it prints the first byte of the opened file.
func TestFortifiedOpenPCIRedirect(t *testing.T) {
	requireLinux(t)
	shim := requireShim(t)
	bin := requireTestBin(t, "open-fortified")

	root := t.TempDir()
	topo := &pcisysfs.PCIeTopology{
		RootComplexes: []pcisysfs.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	// device_id 0x233010DE -> vendor 0x10de, so config[0] == 0xde (222).
	ids := map[string]pcisysfs.PCI{
		"0000:07:00.0": {BusID: "0000:07:00.0", DeviceID: 0x233010DE},
	}
	require.NoError(t, pcisysfs.Render(pcisysfs.Options{Topology: topo, Identities: ids, Output: root}))

	cmd := exec.Command(bin, "0", "/sys/bus/pci/devices/0000:07:00.0/config") // flags 0 == O_RDONLY
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+shim, "MOCK_PCI_ROOT="+root)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "fortified open failed (config not redirected): %s", out)
	require.Equal(t, "222\n", string(out), "config[0] should be 0xde (222) from the mock tree")
}

// TestFopenPCIRedirect guards the fopen hook. libpci reads a device's
// `resource` file via fopen() (used by `lspci -v`), and glibc's fopen goes
// straight to an internal, non-interposable open — so without a dedicated
// fopen interposer the read escapes redirection and hits the real host path.
// Go never calls fopen, so fopen-read exercises the hook.
func TestFopenPCIRedirect(t *testing.T) {
	requireLinux(t)
	shim := requireShim(t)
	bin := requireTestBin(t, "fopen-read")

	root := t.TempDir()
	topo := &pcisysfs.PCIeTopology{
		RootComplexes: []pcisysfs.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	// device_id 0x233010DE -> vendor 0x10de, so config[0] == 0xde (222).
	ids := map[string]pcisysfs.PCI{
		"0000:07:00.0": {BusID: "0000:07:00.0", DeviceID: 0x233010DE},
	}
	require.NoError(t, pcisysfs.Render(pcisysfs.Options{Topology: topo, Identities: ids, Output: root}))

	cmd := exec.Command(bin, "/sys/bus/pci/devices/0000:07:00.0/config")
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+shim, "MOCK_PCI_ROOT="+root)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "fopen failed (config not redirected): %s", out)
	require.Equal(t, "222\n", string(out), "config[0] should be 0xde (222) via the fopen hook")
}

// TestRewriteOverflowFailsClosed asserts that when MOCK_PCI_ROOT is so long
// the rewritten path would overflow the buffer, the shim fails the call with
// ENAMETOOLONG instead of silently falling back to the real host path.
func TestRewriteOverflowFailsClosed(t *testing.T) {
	requireLinux(t)
	shim := requireShim(t)

	// A ~4090-byte root guarantees root_len + len(matched path) + 1 exceeds the
	// shim's PATH_MAX (4096) buffer, forcing the overflow branch.
	longRoot := "/" + strings.Repeat("a", 4090)
	cmd := exec.Command("readlink", "-v", "/sys/bus/pci/devices/0000:07:00.0")
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+shim, "MOCK_PCI_ROOT="+longRoot)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "expected readlink to fail with ENAMETOOLONG, got: %s", out)
	require.Contains(t, strings.ToLower(string(out)), "too long",
		"expected an ENAMETOOLONG error, got: %s", out)
}
