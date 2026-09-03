// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

// ─── buildTopology ───────────────────────────────────────────────────────────

func TestBuildTopology_NilWhenNoDevices(t *testing.T) {
	// No root complexes AND no BDFs: nothing to hang off a fallback root.
	require.Nil(t, buildTopology(&agent.State{}))
	require.Nil(t, buildTopology(&agent.State{
		Devices: []agent.DeviceSpec{{Index: 0, PCIBusID: ""}},
	}))
}

func TestBuildTopology_FlatDefaultWhenNoRootComplexes(t *testing.T) {
	// A config with devices but no pcie_topology block must still render; the
	// profile helper in tests/e2e/go/profile counts on exactly one root here.
	state := &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, PCIBusID: "0000:1A:00.0"},
			{Index: 1, PCIBusID: ""},
			{Index: 2, PCIBusID: "0000:1B:00.0"},
		},
	}

	topo := buildTopology(state)
	require.NotNil(t, topo)
	require.Len(t, topo.RootComplexes, 1)

	rc := topo.RootComplexes[0]
	require.Equal(t, defaultRootComplexID, rc.ID)
	require.Equal(t, 0, rc.NUMANode)
	// BDFs are lowercased to match the keys buildIdentities emits; the device
	// without one is skipped rather than rendered as an empty entry.
	require.Equal(t, []string{"0000:1a:00.0", "0000:1b:00.0"}, rc.Devices)
}

func TestBuildTopology_SingleRC(t *testing.T) {
	state := &agent.State{
		NodeShape: agent.NodeShape{
			Topology: agent.PCIeTopology{
				RootComplexes: []agent.RootComplex{
					{ID: "pci0000:00", NUMANode: 2, DeviceBDFs: []string{"0000:07:00.0", "0000:07:00.1"}},
				},
			},
		},
	}

	topo := buildTopology(state)
	require.NotNil(t, topo)
	require.Len(t, topo.RootComplexes, 1)

	rc := topo.RootComplexes[0]
	require.Equal(t, "pci0000:00", rc.ID)
	require.Equal(t, 2, rc.NUMANode)
	require.Equal(t, []string{"0000:07:00.0", "0000:07:00.1"}, rc.Devices)
}

func TestBuildTopology_MultipleRCs(t *testing.T) {
	state := &agent.State{
		NodeShape: agent.NodeShape{
			Topology: agent.PCIeTopology{
				RootComplexes: []agent.RootComplex{
					{ID: "pci0000:00", NUMANode: 0, DeviceBDFs: []string{"0000:03:00.0"}},
					{ID: "pci0001:00", NUMANode: 1, DeviceBDFs: []string{"0001:05:00.0", "0001:05:00.1"}},
				},
			},
		},
	}

	topo := buildTopology(state)
	require.NotNil(t, topo)
	require.Len(t, topo.RootComplexes, 2)
	require.Equal(t, "pci0000:00", topo.RootComplexes[0].ID)
	require.Equal(t, "pci0001:00", topo.RootComplexes[1].ID)
}

// ─── buildIdentities ─────────────────────────────────────────────────────────

func TestBuildIdentities_EmptyWhenNoDevices(t *testing.T) {
	ids := buildIdentities(&agent.State{})
	require.Empty(t, ids)
}

func TestBuildIdentities_SkipsEmptyBusID(t *testing.T) {
	state := &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, PCIBusID: "", PCIDeviceID: 0x232010de},
		},
	}
	ids := buildIdentities(state)
	require.Empty(t, ids)
}

func TestBuildIdentities_LowercasesKey(t *testing.T) {
	state := &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, PCIBusID: "0000:0B:00.0", PCIDeviceID: 0x232010de},
		},
	}
	ids := buildIdentities(state)
	require.Len(t, ids, 1)

	entry, ok := ids["0000:0b:00.0"]
	require.True(t, ok, "key must be lowercase")
	// BusID in value is kept as-is; DeviceID is copied verbatim.
	require.Equal(t, pcisysfs.PCI{BusID: "0000:0B:00.0", DeviceID: 0x232010de}, entry)
}

func TestBuildIdentities_MultipleDevices(t *testing.T) {
	state := &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, PCIBusID: "0000:07:00.0", PCIDeviceID: 0x232010de},
			{Index: 1, PCIBusID: "0000:07:00.1", PCIDeviceID: 0x233010de},
		},
	}
	ids := buildIdentities(state)
	require.Len(t, ids, 2)
	require.Contains(t, ids, "0000:07:00.0")
	require.Contains(t, ids, "0000:07:00.1")
}

func TestBuildIdentities_CarriesFullIdentity(t *testing.T) {
	state := &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, PCIBusID: "0000:07:00.0", PCIDeviceID: 0x233010DE, PCISubsystemID: 0x165810DE},
		},
	}

	// Every identity word the renderer unpacks must survive the mapping; a
	// dropped one renders as a plausible default rather than an error.
	require.Equal(t,
		pcisysfs.PCI{BusID: "0000:07:00.0", DeviceID: 0x233010DE, SubsystemID: 0x165810DE},
		buildIdentities(state)["0000:07:00.0"])
}

// ─── stageSysfs ──────────────────────────────────────────────────────────────

func TestStageSysfs_NilTopologyIsNop(t *testing.T) {
	h := testHost(t)
	require.NoError(t, stageSysfs(h, &agent.State{}))

	_, err := os.Stat(h.RootPath("sys"))
	require.True(t, os.IsNotExist(err), "sys/ must not be created when state has no root complexes")
}

func TestStageSysfs_WritesSysfsUnderRoot(t *testing.T) {
	h := testHost(t)
	state := stateWithTopology()

	require.NoError(t, stageSysfs(h, state))

	symlink := h.RootPath("sys/bus/pci/devices/0000:07:00.0")
	_, err := os.Lstat(symlink)
	require.NoError(t, err, "BDF symlink must appear under h.Root")
}

func TestStageSysfs_RendersSubsystemAttrs(t *testing.T) {
	h := testHost(t)
	require.NoError(t, stageSysfs(h, stateWithTopology()))

	devDir := h.RootPath("sys/bus/pci/devices/0000:07:00.0")
	for _, tc := range []struct{ file, want string }{
		{"vendor", "0x10de\n"},
		{"device", "0x2330\n"},
		{"subsystem_vendor", "0x10de\n"},
		{"subsystem_device", "0x1658\n"},
	} {
		data, err := os.ReadFile(filepath.Join(devDir, tc.file))
		require.NoError(t, err, "reading %s", tc.file)
		require.Equal(t, tc.want, string(data), tc.file)
	}
}

func TestStageSysfs_RendersFlatDefault(t *testing.T) {
	h := testHost(t)
	state := &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, PCIBusID: "0000:1A:00.0", PCIDeviceID: 0x233010DE, PCISubsystemID: 0x165810DE},
		},
	}

	require.NoError(t, stageSysfs(h, state))

	target, err := os.Readlink(h.RootPath("sys/bus/pci/devices/0000:1a:00.0"))
	require.NoError(t, err, "device must be rendered under the synthesized root")
	require.Contains(t, target, defaultRootComplexID)
}

// ─── stagePCIShim ────────────────────────────────────────────────────────────

// withShimGlob points stagePCIShim at dir for the duration of the test, so both
// branches run regardless of what the host has under /usr/local/lib.
func withShimGlob(t *testing.T, dir string) {
	t.Helper()
	orig := shimGlob
	shimGlob = filepath.Join(dir, "libpcisysfs.so*")
	t.Cleanup(func() { shimGlob = orig })
}

func TestStagePCIShim_NopWhenNoLib(t *testing.T) {
	withShimGlob(t, t.TempDir())

	h := testHost(t)
	require.NoError(t, stagePCIShim(h))

	libDir := h.RootPath("driver/usr/local/lib")
	_, err := os.Stat(libDir)
	require.True(t, os.IsNotExist(err), "lib dir must not be created when shim is absent")
}

func TestStagePCIShim_StagesEverySoname(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"libpcisysfs.so", "libpcisysfs.so.1"} {
		require.NoError(t, os.WriteFile(filepath.Join(src, name), []byte(name), 0o755))
	}
	withShimGlob(t, src)

	h := testHost(t)
	require.NoError(t, stagePCIShim(h))

	// The NRI plugin LD_PRELOADs the versioned soname, so every match must land
	// in the driver lib dir, not only the first.
	for _, name := range []string{"libpcisysfs.so", "libpcisysfs.so.1"} {
		data, err := os.ReadFile(h.RootPath("driver/usr/local/lib", name))
		require.NoError(t, err, "%s must be staged", name)
		require.Equal(t, name, string(data))
	}
}
