// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"os"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/numatopology"
	"github.com/stretchr/testify/require"
)

func TestResolvedStateIsSharedWithoutChangingInput(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	input := testState()
	original := string(input.ConfigRaw)
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	state, err := numatopology.ResolveState(t.Context(), h, input)
	require.NoError(t, err)
	require.NotSame(t, input, state)
	require.Empty(t, input.NodeShape.Topology.RootComplexes[0].CPUAffinity)
	require.Equal(t, original, string(input.ConfigRaw))
	require.Nil(t, input.NodeShape.NUMA)
	require.Equal(t, []agent.NUMAZone{
		{ID: 0, CPUs: []int{0, 1, 4, 5}, MemoryKB: 8000},
		{ID: 1, CPUs: []int{2, 3, 6, 7}, MemoryKB: 8000},
	}, state.NodeShape.NUMA.Zones)
	require.Equal(t, "0-1,4-5", state.NodeShape.Topology.RootComplexes[0].CPUAffinity)
	require.Contains(t, string(state.ConfigRaw), "cpu_affinity: 0-1,4-5")
	// Stage must consume the shared plan, without reading host topology again.
	require.NoError(t, os.Remove(h.ProcPath("meminfo")))
	require.NoError(t, os.Remove(h.SysPath("devices/system/cpu/online")))
	require.NoError(t, sim.Stage(t.Context(), state))
	requireFileContent(t, h.RootPath("numa/sysnode/node0/cpulist"), "0-1,4-5\n")
	requireFileContent(t, h.RootPath("numa/sysnode/node1/cpulist"), "2-3,6-7\n")
}

func TestStageRequiresPreparedTopology(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	require.ErrorContains(t, sim.Stage(t.Context(), testState()), "NUMA topology is not prepared")
	require.NoDirExists(t, h.RootPath("numa/sysnode"))
}

func TestStateResolutionHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := numatopology.ResolveState(ctx, host.New(t.TempDir()), &agent.State{})
	require.ErrorIs(t, err, context.Canceled)
}
