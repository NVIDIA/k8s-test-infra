// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

// stagedPCITree is an overlay carrying what the pcibus renderer stages.
func stagedPCITree(t *testing.T) Config {
	t.Helper()

	root := t.TempDir()
	for _, relPath := range []string{pcisysfs.SysDevicesRelPath, pcisysfs.PCIDevicesRelPath} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, relPath), 0o755))
	}

	return Config{HostOverlayPath: root}
}

// The RDMA device plugin finds its HCAs by enumerating the PCI bus and matching
// the Mellanox vendor, then reads the infiniband directory under each device it
// matched. #673 serves that tree through CDI, which never reaches this consumer:
// the plugin requests no GPU, so nothing resolves a CDI spec for its pod, and it
// reads the node's real bus instead — where a CPU-only node has no Mellanox
// device at all and the plugin advertises nothing.
func TestAdjust_ServesThePCITreeAtTheKernelPaths(t *testing.T) {
	t.Parallel()

	cfg := stagedPCITree(t)

	adjustment, ok := Adjust(cfg, Container{Namespace: "default"})
	require.True(t, ok)

	devices := mountFor(t, adjustment.Mounts, "/sys/devices")
	require.Equal(t, filepath.Join(cfg.HostOverlayPath, pcisysfs.SysDevicesRelPath), devices.Source)

	bus := mountFor(t, adjustment.Mounts, "/sys/bus/pci/devices")
	require.Equal(t, filepath.Join(cfg.HostOverlayPath, pcisysfs.PCIDevicesRelPath), bus.Source)
}

// Same fail-open rule as every other surface: nothing orders this plugin's
// DaemonSet after the agent's, and a mount whose source does not exist fails
// creation for the whole pod rather than for the tree it belongs to.
func TestAdjust_OmitsThePCITreeWhenNoneIsStaged(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(Config{HostOverlayPath: t.TempDir()}, Container{Namespace: "default"})
	require.True(t, ok)

	for _, m := range adjustment.Mounts {
		require.NotEqual(t, "/sys/devices", m.Destination)
		require.NotEqual(t, "/sys/bus/pci/devices", m.Destination)
	}
}

// Serving the mock hierarchy at /sys/devices hides the node's own, and every
// /sys/class/net entry is a relative symlink into it — so reading an interface
// attribute through the class directory fails, for the node's interfaces and
// for the simulated HCAs' links alike. The node's own netdev hierarchy goes
// back over the mountpoint the renderer left, which is also where the HCAs'
// links live: they are real dummy netdevs in the node's namespace.
func TestAdjust_ServesTheNodesNetdevsBackOverTheMockHierarchy(t *testing.T) {
	t.Parallel()

	cfg := stagedPCITree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(cfg.HostOverlayPath, pcisysfs.VirtualNetRelPath), 0o755))

	adjustment, ok := Adjust(cfg, Container{Namespace: "default"})
	require.True(t, ok)

	virtualNet := "/" + pcisysfs.VirtualNetRelPath
	netdevs := mountFor(t, adjustment.Mounts, virtualNet)
	require.Equal(t, virtualNet, netdevs.Source, "the node's own hierarchy, served back at its own path")

	// Ordering: it lands inside the hierarchy mount, so it has to come after.
	hierarchy, reproduced := -1, -1
	for i, m := range adjustment.Mounts {
		switch m.Destination {
		case "/" + pcisysfs.SysDevicesRelPath:
			hierarchy = i
		case virtualNet:
			reproduced = i
		}
	}

	require.Less(t, hierarchy, reproduced, "the mock hierarchy would cover the netdevs mounted before it")
}

// The bus directory is flat symlinks into the hierarchy, so serving it without
// what it points into leaves every entry dangling.
func TestAdjust_ServesTheBusAndTheHierarchyTogether(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, pcisysfs.PCIDevicesRelPath), 0o755))

	adjustment, ok := Adjust(Config{HostOverlayPath: root}, Container{Namespace: "default"})
	require.True(t, ok)

	for _, m := range adjustment.Mounts {
		require.NotEqual(t, "/sys/bus/pci/devices", m.Destination)
	}
}
