// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package pcibus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs/config"
)

// ─── buildTopology ───────────────────────────────────────────────────────────

func TestBuildTopology_NilWhenEmpty(t *testing.T) {
	require.Nil(t, buildTopology(&agent.State{}))
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
	require.Equal(t, config.PCI{BusID: "0000:0B:00.0", DeviceID: 0x232010de}, entry)
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

// ─── stageSysfs ──────────────────────────────────────────────────────────────

func TestStageSysfs_NilTopologyIsNop(t *testing.T) {
	h := testHost(t)
	require.NoError(t, stageSysfs(h, &agent.State{}))

	_, err := os.Stat(filepath.Join(h.Root, "sys"))
	require.True(t, os.IsNotExist(err), "sys/ must not be created when state has no root complexes")
}

func TestStageSysfs_WritesSysfsUnderRoot(t *testing.T) {
	h := testHost(t)
	state := stateWithTopology()

	require.NoError(t, stageSysfs(h, state))

	symlink := filepath.Join(h.Root, "sys/bus/pci/devices/0000:07:00.0")
	_, err := os.Lstat(symlink)
	require.NoError(t, err, "BDF symlink must appear under h.Root")
}

// ─── stagePCIShim ────────────────────────────────────────────────────────────

func TestStagePCIShim_NopWhenNoLib(t *testing.T) {
	// This passes on any machine that doesn't have libpcimocksys.so built in at
	// /usr/local/lib — i.e., all dev machines and macOS CI runners.
	matches, _ := filepath.Glob("/usr/local/lib/libpcimocksys.so*")
	if len(matches) > 0 {
		t.Skip("libpcimocksys.so present; skipping no-lib path")
	}

	h := testHost(t)
	require.NoError(t, stagePCIShim(h))

	libDir := filepath.Join(h.Root, "driver/usr/local/lib")
	_, err := os.Stat(libDir)
	require.True(t, os.IsNotExist(err), "lib dir must not be created when shim is absent")
}
