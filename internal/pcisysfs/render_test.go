// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcisysfs

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRender_NoTopologyNoOp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Render(Options{Output: dir}), "Render(no topology)")
	entries, _ := os.ReadDir(dir)
	require.Empty(t, entries, "expected empty output for nil topology")
}

func TestRender_RequiresOutput(t *testing.T) {
	err := Render(Options{Topology: &PCIeTopology{
		RootComplexes: []RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}})
	require.Error(t, err, "expected error with empty Output")
}

// TestRender_FullTree exercises the documented sysfs layout end-to-end:
// two GPUs on one root complex must produce a populated devices dir,
// a populated symlink dir, and numa_node files with the configured
// value. This is the regression net for the issue's acceptance test:
// "k8s deviceattribute resolves PCIe root via readlink + path parse".
func TestRender_FullTree(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{
		RootComplexes: []RootComplex{
			{
				ID:       "pci0000:00",
				NUMANode: 0,
				Devices:  []string{"0000:07:00.0", "0000:0F:00.0"},
			},
			{
				ID:       "pci0000:80",
				NUMANode: 1,
				Devices:  []string{"0000:87:00.0"},
			},
		},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")

	// numa_node files must contain the per-root NUMA value (and *only*
	// that — a trailing-newline regression would silently bake "0\n0\n"
	// into a re-render).
	mustRead := func(rel, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(dir, rel))
		require.NoError(t, err, "read %s", rel)
		require.Equal(t, want, string(got), "%s", rel)
	}
	mustRead("sys/devices/pci0000:00/0000:07:00.0/numa_node", "0\n")
	mustRead("sys/devices/pci0000:00/0000:0f:00.0/numa_node", "0\n")
	mustRead("sys/devices/pci0000:80/0000:87:00.0/numa_node", "1\n")

	// The symlink target must be *relative* — the deviceattribute
	// library walks readlink() output to extract the pciDDDD:BB
	// component, which only works when the target is the kernel-style
	// "../../../devices/pciDDDD:BB/<bdf>" string.
	mustLink := func(rel, wantTarget string) {
		t.Helper()
		target, err := os.Readlink(filepath.Join(dir, rel))
		require.NoError(t, err, "readlink %s", rel)
		require.Equal(t, wantTarget, target, "%s", rel)
	}
	mustLink("sys/bus/pci/devices/0000:07:00.0", "../../../devices/pci0000:00/0000:07:00.0")
	mustLink("sys/bus/pci/devices/0000:0f:00.0", "../../../devices/pci0000:00/0000:0f:00.0")
	mustLink("sys/bus/pci/devices/0000:87:00.0", "../../../devices/pci0000:80/0000:87:00.0")

	// Resolving the symlink (the path-parse step the deviceattribute
	// library actually performs) must land on the real numa_node file.
	resolved, err := filepath.EvalSymlinks(
		filepath.Join(dir, "sys/bus/pci/devices/0000:07:00.0"))
	require.NoError(t, err, "EvalSymlinks")
	require.True(t, strings.HasSuffix(resolved, "sys/devices/pci0000:00/0000:07:00.0"),
		"resolved=%q does not land under expected root complex", resolved)
}

// TestRender_PCIAttributeFiles is the regression net for the lspci fix:
// each rendered device must carry the sysfs identity attribute files that
// libpci reads with die-on-error, plus a binary config space. Without them
// `lspci` fails with "Cannot open .../vendor" inside the mock pod.
func TestRender_PCIAttributeFiles(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{
		RootComplexes: []RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:1A:00.0"},
		}},
	}
	ids := map[string]PCI{
		// H100 SXM: device_id 0x233010DE, subsystem_id 0x165810DE.
		"0000:1a:00.0": {BusID: "0000:1A:00.0", DeviceID: 0x233010DE, SubsystemID: 0x165810DE},
	}
	require.NoError(t, Render(Options{Topology: topo, Identities: ids, Output: dir}), "Render")

	devDir := filepath.Join(dir, "sys/devices/pci0000:00/0000:1a:00.0")
	mustRead := func(name, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(devDir, name))
		require.NoError(t, err, "read %s", name)
		require.Equal(t, want, string(got), "%s", name)
	}
	mustRead("vendor", "0x10de\n")
	mustRead("device", "0x2330\n")
	mustRead("subsystem_vendor", "0x10de\n")
	mustRead("subsystem_device", "0x1658\n")
	mustRead("class", "0x030200\n")
	mustRead("revision", "0x00\n")
	mustRead("irq", "0\n")

	// `resource` must match the kernel's 7-row "start end flags" layout so
	// `lspci -v` parses it without erroring.
	resource, err := os.ReadFile(filepath.Join(devDir, "resource"))
	require.NoError(t, err, "read resource")
	lines := strings.Split(strings.TrimRight(string(resource), "\n"), "\n")
	require.Len(t, lines, 7, "resource BAR rows")
	require.Equal(t, "0x0000000000000000 0x0000000000000000 0x0000000000000000", lines[0],
		"resource row format")

	// The binary config space must decode to the same identity so
	// `lspci -x` and the pcilib config-open path agree with the text files.
	cfg, err := os.ReadFile(filepath.Join(devDir, "config"))
	require.NoError(t, err, "read config")
	require.Len(t, cfg, 256, "config space size")
	require.Equal(t, uint16(0x10de), binary.LittleEndian.Uint16(cfg[0x00:]), "config vendor")
	require.Equal(t, uint16(0x2330), binary.LittleEndian.Uint16(cfg[0x02:]), "config device")
	require.Equal(t, byte(0x03), cfg[0x0b], "config class base")
	require.Equal(t, byte(0x02), cfg[0x0a], "config subclass")
	require.Equal(t, uint16(0x10de), binary.LittleEndian.Uint16(cfg[0x2c:]), "config subsystem vendor")
	require.Equal(t, uint16(0x1658), binary.LittleEndian.Uint16(cfg[0x2e:]), "config subsystem device")
}

// TestRender_PCIAttributeFilesDefaultVendor ensures a device present in the
// topology but missing an identity still gets well-formed attribute files
// (NVIDIA vendor default) so lspci never fatals on a missing `vendor`.
func TestRender_PCIAttributeFilesDefaultVendor(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{
		RootComplexes: []RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")

	devDir := filepath.Join(dir, "sys/devices/pci0000:00/0000:07:00.0")
	got, err := os.ReadFile(filepath.Join(devDir, "vendor"))
	require.NoError(t, err, "read vendor")
	require.Equal(t, "0x10de\n", string(got), "vendor should default to NVIDIA")
	got, err = os.ReadFile(filepath.Join(devDir, "device"))
	require.NoError(t, err, "read device")
	require.Equal(t, "0x0000\n", string(got), "device without identity")
}

func TestRender_IdempotentRerender(t *testing.T) {
	dir := t.TempDir()
	topoA := &PCIeTopology{
		RootComplexes: []RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	// First pass.
	require.NoError(t, Render(Options{Topology: topoA, Output: dir}), "Render pass 1")

	// Second pass with a *different* root complex for the same BDF.
	// Re-render must point the symlink at the new root and overwrite
	// numa_node — a stale symlink would silently misattribute pcieRoot.
	topoB := &PCIeTopology{
		RootComplexes: []RootComplex{{
			ID: "pci0000:c0", NUMANode: 3,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topoB, Output: dir}), "Render pass 2")

	target, err := os.Readlink(filepath.Join(dir, "sys/bus/pci/devices/0000:07:00.0"))
	require.NoError(t, err, "readlink")
	require.Equal(t, "../../../devices/pci0000:c0/0000:07:00.0", target,
		"symlink target stale across rerender")
	got, err := os.ReadFile(filepath.Join(dir, "sys/devices/pci0000:c0/0000:07:00.0/numa_node"))
	require.NoError(t, err, "read numa_node")
	require.Equal(t, "3\n", string(got), "numa_node not updated")
}

func TestRender_NormalizesUppercaseBDF(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{
		RootComplexes: []RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:BD:00.0"}, // uppercase BDF
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")
	// Real sysfs is lowercase. Render must lowercase before writing so
	// downstream tools (lspci, libpciaccess) that string-compare BDFs
	// see what they expect.
	_, err := os.Stat(filepath.Join(dir, "sys/bus/pci/devices/0000:bd:00.0"))
	require.NoError(t, err, "expected lowercase symlink")
}

// TestRender_PrunesRemovedDevices covers the converging half of Render's
// contract: a re-render with a shrunk device set must leave no orphan behind.
// Without it lspci keeps enumerating GPUs that NVML no longer reports.
func TestRender_PrunesRemovedDevices(t *testing.T) {
	dir := t.TempDir()
	rc := func(devs ...string) *PCIeTopology {
		return &PCIeTopology{RootComplexes: []RootComplex{
			{ID: "pci0000:00", NUMANode: 0, Devices: devs},
		}}
	}

	require.NoError(t, Render(Options{
		Topology: rc("0000:07:00.0", "0000:0f:00.0", "0000:17:00.0"),
		Output:   dir,
	}))
	require.NoError(t, Render(Options{Topology: rc("0000:07:00.0"), Output: dir}))

	require.FileExists(t, filepath.Join(dir, "sys/devices/pci0000:00/0000:07:00.0/vendor"))
	require.DirExists(t, filepath.Join(dir, "sys/devices/pci0000:00/0000:07:00.0"))

	for _, gone := range []string{"0000:0f:00.0", "0000:17:00.0"} {
		require.NoDirExists(t, filepath.Join(dir, "sys/devices/pci0000:00", gone))
		_, err := os.Lstat(filepath.Join(dir, "sys/bus/pci/devices", gone))
		require.True(t, os.IsNotExist(err), "stale symlink %s must be pruned", gone)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "sys/bus/pci/devices"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly the surviving device")
}

func TestRender_PrunesRemovedRootComplex(t *testing.T) {
	dir := t.TempDir()
	two := &PCIeTopology{RootComplexes: []RootComplex{
		{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
		{ID: "pci0001:00", NUMANode: 1, Devices: []string{"0001:05:00.0"}},
	}}
	one := &PCIeTopology{RootComplexes: []RootComplex{
		{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
	}}

	require.NoError(t, Render(Options{Topology: two, Output: dir}))
	require.NoError(t, Render(Options{Topology: one, Output: dir}))

	require.DirExists(t, filepath.Join(dir, "sys/devices/pci0000:00"))
	require.NoDirExists(t, filepath.Join(dir, "sys/devices/pci0001:00"))
	_, err := os.Lstat(filepath.Join(dir, "sys/bus/pci/devices/0001:05:00.0"))
	require.True(t, os.IsNotExist(err), "symlink into the dropped root must be pruned")
}

// Re-profiling onto a config whose devices declare no bus_id, which
// gpu.customConfig makes reachable. Now that the tree is served at the kernel
// paths, leftovers would have a consumer enumerate GPUs the node no longer
// simulates.
func TestRender_ClearsTreeWhenTopologyEmpty(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{RootComplexes: []RootComplex{
		{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
	}}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}))

	require.NoError(t, Render(Options{Output: dir}), "Render(empty topology)")

	require.NoDirExists(t, filepath.Join(dir, "sys/devices/pci0000:00"))
	entries, err := os.ReadDir(filepath.Join(dir, PCIDevicesRelPath))
	require.NoError(t, err)
	require.Empty(t, entries, "no device may survive a profile that declares none")
}

// The ownership boundary holds on the clearing path too: virtual/dmi/id is
// staged by the same simulator for another reason, and a served container needs
// it to start.
func TestRender_EmptyTopologyKeepsForeignEntries(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{RootComplexes: []RootComplex{
		{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
	}}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}))

	foreign := filepath.Join(dir, SysDevicesRelPath, "virtual/dmi/id")
	require.NoError(t, os.MkdirAll(foreign, 0o755))

	require.NoError(t, Render(Options{Output: dir}), "Render(empty topology)")

	require.DirExists(t, foreign)
}

// TestRender_PruneLeavesForeignEntriesAlone guards the ownership boundary:
// libpcisysfs rewrites only /sys/devices/pci*, so the renderer must not delete
// anything else a sibling component staged in the same fake root.
func TestRender_PruneLeavesForeignEntriesAlone(t *testing.T) {
	dir := t.TempDir()
	topo := &PCIeTopology{RootComplexes: []RootComplex{
		{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
	}}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}))

	foreign := filepath.Join(dir, "sys/devices/platform")
	require.NoError(t, os.MkdirAll(foreign, 0o755))
	require.NoError(t, Render(Options{Topology: topo, Output: dir}))

	require.DirExists(t, foreign, "non-pci entries are not this renderer's to remove")
}
