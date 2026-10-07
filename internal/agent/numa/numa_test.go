// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
)

// twoSocketState declares a root complex on each of two NUMA nodes.
func twoSocketState() *agent.State {
	return &agent.State{NodeShape: agent.NodeShape{Topology: agent.PCIeTopology{RootComplexes: []agent.RootComplex{
		{ID: "pci0000:00", NUMANode: 0},
		{ID: "pci0008:00", NUMANode: 1},
	}}}}
}

func listNodes(t *testing.T, entry string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(entry, "devices"))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestStage_ServesOneNodePerDeclaredNUMANode(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()

	require.NoError(t, stage(entry, twoSocketState()))

	require.Equal(t, []string{"node0", "node1"}, listNodes(t, entry))
}

// The kernel links each entry to its device; the target is left absent rather
// than invented, since kubelet reads /sys/devices/system/node for topology.
func TestStage_LinksEachNodeWhereTheKernelWould(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()

	require.NoError(t, stage(entry, twoSocketState()))

	target, err := os.Readlink(filepath.Join(entry, "devices/node1"))
	require.NoError(t, err)
	require.Equal(t, "../../../devices/system/node/node1", target)
}

// Every Linux system has node0, NUMA-capable or not.
func TestStage_ServesNodeZeroWithoutATopology(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()

	require.NoError(t, stage(entry, &agent.State{}))

	require.Equal(t, []string{"node0"}, listNodes(t, entry))
}

// -1 is Linux's "no proximity information", not a node.
func TestStage_IgnoresAnUnknownNUMANode(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()
	state := &agent.State{NodeShape: agent.NodeShape{Topology: agent.PCIeTopology{RootComplexes: []agent.RootComplex{
		{ID: "pci0000:00", NUMANode: -1},
	}}}}

	require.NoError(t, stage(entry, state))

	require.Equal(t, []string{"node0"}, listNodes(t, entry))
}

// Once served, the staged entry is what the node shows, so a profile with
// fewer NUMA nodes must not leave the old ones behind.
func TestStage_DropsNodesAProfileNoLongerDeclares(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()
	require.NoError(t, stage(entry, twoSocketState()))

	require.NoError(t, stage(entry, &agent.State{}))

	require.Equal(t, []string{"node0"}, listNodes(t, entry))
}
