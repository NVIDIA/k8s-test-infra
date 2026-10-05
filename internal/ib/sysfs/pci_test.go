// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
)

func readTrimmed(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err, "read %s", rel)
	return strings.TrimSpace(string(b))
}

func slotName(t *testing.T, root, ca string) string {
	t.Helper()
	uevent := readTrimmed(t, root, filepath.Join("sys/class/infiniband", ca, "device/uevent"))
	for line := range strings.SplitSeq(uevent, "\n") {
		if v, ok := strings.CutPrefix(line, "PCI_SLOT_NAME="); ok {
			return v
		}
	}
	require.Failf(t, "no PCI_SLOT_NAME", "uevent of %s: %q", ca, uevent)
	return ""
}

func TestRender_PCIDeviceIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, Render(Options{
		IB:      config.Infiniband{Enabled: true, HCACountOverride: 1},
		RootDir: dir,
	}))

	dev := "sys/class/infiniband/mlx5_0/device"
	require.Equal(t, "0x15b3", readTrimmed(t, dir, filepath.Join(dev, "vendor")))
	require.Equal(t, "0x1017", readTrimmed(t, dir, filepath.Join(dev, "device")))

	// The uevent must agree with the modalias libibverbs matches on.
	uevent := readTrimmed(t, dir, filepath.Join(dev, "uevent"))
	require.Contains(t, uevent, "DRIVER=mlx5_core\n")
	require.Contains(t, uevent, "PCI_ID=15B3:1017\n")
	require.Contains(t, uevent, "MODALIAS="+readTrimmed(t, dir, filepath.Join(dev, "modalias")))

	// A physical function never carries physfn; its presence marks an SR-IOV VF.
	_, err := os.Lstat(filepath.Join(dir, dev, "physfn"))
	require.True(t, os.IsNotExist(err), "physfn on a PF: %v", err)

	// device/ is the PCI function, which lists the IB device it backs.
	require.FileExists(t, filepath.Join(dir, dev, "infiniband/mlx5_0/node_guid"))
}

func TestRender_HCAsSitBesideTheirGPUs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, Render(Options{
		IB: config.Infiniband{Enabled: true, HCACountOverride: 2},
		GPUs: []PCIFunction{
			{Address: "0000:07:00.0", NUMANode: 0},
			{Address: "0000:87:00.0", NUMANode: 1},
		},
		RootDir: dir,
	}))

	require.Equal(t, "0000:08:00.0", slotName(t, dir, "mlx5_0"))
	require.Equal(t, "0", readTrimmed(t, dir, "sys/class/infiniband/mlx5_0/device/numa_node"))
	require.Equal(t, "0000:88:00.0", slotName(t, dir, "mlx5_1"))
	require.Equal(t, "1", readTrimmed(t, dir, "sys/class/infiniband/mlx5_1/device/numa_node"))
}

func TestRender_HCAsSharingAGPUGetSeparateCards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: 2},
		GPUs:     []PCIFunction{{Address: "0000:07:00.0", NUMANode: 3}},
		Occupied: []string{"0000:08:00.0"}, // e.g. an NVSwitch
		RootDir:  dir,
	}))

	require.Equal(t, "0000:09:00.0", slotName(t, dir, "mlx5_0"))
	require.Equal(t, "0000:0a:00.0", slotName(t, dir, "mlx5_1"))
	for _, ca := range []string{"mlx5_0", "mlx5_1"} {
		require.Equal(t, "3", readTrimmed(t, dir, filepath.Join("sys/class/infiniband", ca, "device/numa_node")))
	}
}

func TestRender_HCAWithoutGPUHasUnknownNUMANode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, Render(Options{
		IB:      config.Infiniband{Enabled: true, HCACountOverride: 2},
		RootDir: dir,
	}))

	require.Equal(t, "-1", readTrimmed(t, dir, "sys/class/infiniband/mlx5_0/device/numa_node"))
	require.NotEqual(t, slotName(t, dir, "mlx5_0"), slotName(t, dir, "mlx5_1"))
}

func TestRender_FailsWhenNoPCIBusIsFree(t *testing.T) {
	t.Parallel()
	occupied := make([]string, 0, 255)
	for bus := 1; bus <= 0xff; bus++ {
		occupied = append(occupied, fmt.Sprintf("0000:%02x:00.0", bus))
	}
	err := Render(Options{
		IB:       config.Infiniband{Enabled: true, HCACountOverride: 1},
		Occupied: occupied,
		RootDir:  t.TempDir(),
	})
	require.ErrorContains(t, err, "no free PCI bus")
}

func TestRender_RejectsMalformedGPUAddress(t *testing.T) {
	t.Parallel()
	err := Render(Options{
		IB:      config.Infiniband{Enabled: true, HCACountOverride: 1},
		GPUs:    []PCIFunction{{Address: "07:00.0"}},
		RootDir: t.TempDir(),
	})
	require.ErrorContains(t, err, "07:00.0")
}
