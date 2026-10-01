// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// grep -H over two HCAs, each placed beside a GPU on its own NUMA node.
const hcaFilesTwoSockets = `infiniband/mlx5_0/device/vendor:0x15b3
infiniband/mlx5_0/device/numa_node:0
infiniband/mlx5_0/device/uevent:DRIVER=mlx5_core
infiniband/mlx5_0/device/uevent:PCI_SLOT_NAME=0000:08:00.0
infiniband/mlx5_0/ports/1/counters/port_xmit_wait:0
infiniband/mlx5_0/ports/1/hw_counters/rnr_nak_retry_err:0
infiniband/mlx5_1/device/vendor:0x15b3
infiniband/mlx5_1/device/numa_node:1
infiniband/mlx5_1/device/uevent:PCI_SLOT_NAME=0000:88:00.0
infiniband/mlx5_1/ports/1/counters/port_xmit_wait:0
infiniband/mlx5_1/ports/1/hw_counters/rnr_nak_retry_err:0
`

var (
	twoSocketGPUNUMA = []int{0, 1}
	twoSocketPCI     = []string{"0000:07:00.0", "0000:87:00.0"}
)

func TestParseHCAFilesGroupsAttributesByHCA(t *testing.T) {
	t.Parallel()

	require.Equal(t, []nicRow{
		{name: "mlx5_0", vendor: "0x15b3", numa: "0", slot: "0000:08:00.0", xmitWait: "0", rnrNakRetry: "0"},
		{name: "mlx5_1", vendor: "0x15b3", numa: "1", slot: "0000:88:00.0", xmitWait: "0", rnrNakRetry: "0"},
	}, parseHCAFiles(hcaFilesTwoSockets))
}

func TestNICSysfsProblemsAcceptsAHealthyTree(t *testing.T) {
	t.Parallel()

	require.Empty(t, nicSysfsProblems(parseHCAFiles(hcaFilesTwoSockets), 2, twoSocketGPUNUMA, twoSocketPCI))
}

func TestNICSysfsProblemsFlagsTheWrongHCACount(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, nicSysfsProblems(parseHCAFiles(hcaFilesTwoSockets), 4, twoSocketGPUNUMA, twoSocketPCI))
}

func TestNICSysfsProblemsFlagsEachBrokenAttribute(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(n *nicRow)
		want   string
	}{
		{"wrong vendor", func(n *nicRow) { n.vendor = "0x8086" }, "device/vendor"},
		{"missing counter", func(n *nicRow) { n.xmitWait = "" }, "port_xmit_wait"},
		{"missing hw counter", func(n *nicRow) { n.rnrNakRetry = "" }, "rnr_nak_retry_err"},
		{"missing slot", func(n *nicRow) { n.slot = "" }, "PCI_SLOT_NAME"},
		{"slot the profile already declares", func(n *nicRow) { n.slot = "0000:07:00.0" }, "PCI_SLOT_NAME"},
		{"missing NUMA node", func(n *nicRow) { n.numa = "" }, "numa_node"},
		{"NUMA node no GPU uses", func(n *nicRow) { n.numa = "3" }, "numa_node"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			nics := parseHCAFiles(hcaFilesTwoSockets)
			c.mutate(&nics[0])

			problems := nicSysfsProblems(nics, 2, twoSocketGPUNUMA, twoSocketPCI)
			require.NotEmpty(t, problems)
			require.Contains(t, problems[0], "mlx5_0")
			require.Contains(t, problems[0], c.want)
		})
	}
}

func TestNICSysfsProblemsFlagsSharedSlots(t *testing.T) {
	t.Parallel()
	nics := parseHCAFiles(hcaFilesTwoSockets)
	nics[1].slot = nics[0].slot

	require.Contains(t, nicSysfsProblems(nics, 2, twoSocketGPUNUMA, twoSocketPCI),
		"mlx5_0 and mlx5_1 share PCI_SLOT_NAME 0000:08:00.0")
}

// A monitor tells a GPU's compute NIC apart by its NUMA node, so a socket with
// GPUs and no NIC means some GPU has none.
func TestNICSysfsProblemsFlagsASocketWithoutNIC(t *testing.T) {
	t.Parallel()
	nics := parseHCAFiles(hcaFilesTwoSockets)
	nics[1].numa = "0"

	require.Contains(t, nicSysfsProblems(nics, 2, twoSocketGPUNUMA, twoSocketPCI),
		"NUMA node 1 has GPUs but no NIC")
}
