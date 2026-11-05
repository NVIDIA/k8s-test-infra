// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
)

func renderRoCE(t *testing.T, dir string, ib config.Infiniband) {
	t.Helper()
	ib.Enabled = true
	ib.LinkLayer = "Ethernet"
	ib.HCACountOverride = 1
	require.NoError(t, Render(Options{
		IB:      ib,
		GPUs:    []PCIFunction{{Address: "0000:07:00.0", NUMANode: 0}},
		RootDir: dir,
	}))
}

func TestRender_PortExposesHealthCounters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, Render(Options{
		IB:      config.Infiniband{Enabled: true, HCACountOverride: 1},
		RootDir: dir,
	}))

	port := "sys/class/infiniband/mlx5_0/ports/1"
	require.Equal(t, "0", readTrimmed(t, dir, filepath.Join(port, "counters/port_xmit_wait")))
	for _, c := range []string{
		"implied_nak_seq_err", "local_ack_timeout_err", "out_of_sequence",
		"packet_seq_err", "rnr_nak_retry_err", "roce_slow_restart",
	} {
		require.Equal(t, "0", readTrimmed(t, dir, filepath.Join(port, "hw_counters", c)), c)
	}
}

func TestRender_RoCEHCAHasNetdev(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	renderRoCE(t, dir, config.Infiniband{})

	const netdev = "enp8s0np0" // systemd's name for the function at 0000:08:00.0
	ca := "sys/class/infiniband/mlx5_0"
	net := filepath.Join("sys/class/net", netdev)

	require.Equal(t, "up", readTrimmed(t, dir, filepath.Join(net, "operstate")))
	require.Equal(t, "1", readTrimmed(t, dir, filepath.Join(net, "carrier")))
	require.Equal(t, "0", readTrimmed(t, dir, filepath.Join(net, "carrier_changes")))
	for _, c := range []string{"rx_crc_errors", "rx_errors", "rx_missed_errors", "tx_carrier_errors", "tx_errors"} {
		require.Equal(t, "0", readTrimmed(t, dir, filepath.Join(net, "statistics", c)), c)
	}

	// Both directions resolve, as they do through the kernel's device links:
	// the HCA names its netdev, and the netdev names the HCA it belongs to.
	require.Equal(t, "up", readTrimmed(t, dir, filepath.Join(ca, "device/net", netdev, "operstate")))
	require.Equal(t, "Ethernet", readTrimmed(t, dir, filepath.Join(net, "device/infiniband/mlx5_0/ports/1/link_layer")))
}

func TestRender_RoCENetdevFollowsPortState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	renderRoCE(t, dir, config.Infiniband{PortState: "DOWN", PhysState: "Disabled"})

	require.Equal(t, "down", readTrimmed(t, dir, "sys/class/net/enp8s0np0/operstate"))
	require.Equal(t, "0", readTrimmed(t, dir, "sys/class/net/enp8s0np0/carrier"))
}

func TestRender_InfiniBandHCAHasNoNetdev(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, Render(Options{
		IB:      config.Infiniband{Enabled: true, HCACountOverride: 1},
		RootDir: dir,
	}))

	entries, err := os.ReadDir(filepath.Join(dir, "sys/class/net"))
	require.NoError(t, err, "class/net exists on every node, with or without RoCE")
	require.Empty(t, entries)
	require.NoDirExists(t, filepath.Join(dir, "sys/class/infiniband/mlx5_0/device/net"))
}

func TestRender_SwitchingToInfiniBandRetractsNetdev(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	renderRoCE(t, dir, config.Infiniband{})
	require.DirExists(t, filepath.Join(dir, "sys/class/net/enp8s0np0"))

	require.NoError(t, Render(Options{
		IB:      config.Infiniband{Enabled: true, HCACountOverride: 1},
		GPUs:    []PCIFunction{{Address: "0000:07:00.0", NUMANode: 0}},
		RootDir: dir,
	}))

	_, err := os.Lstat(filepath.Join(dir, "sys/class/net/enp8s0np0"))
	require.True(t, os.IsNotExist(err), "stale netdev: %v", err)
	_, err = os.Lstat(filepath.Join(dir, "sys/class/infiniband/mlx5_0/device/net"))
	require.True(t, os.IsNotExist(err), "stale device/net: %v", err)
}

func TestRender_RerenderLeavesLinksInPlace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	renderRoCE(t, dir, config.Infiniband{})
	link := filepath.Join(dir, "sys/class/net/enp8s0np0/device")
	before, err := os.Lstat(link)
	require.NoError(t, err)

	renderRoCE(t, dir, config.Infiniband{})

	after, err := os.Lstat(link)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "an unchanged link must not be recreated under a reader")
}
