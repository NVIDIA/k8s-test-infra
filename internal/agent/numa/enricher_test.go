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

func TestEnricherWritesOnlyTheLayoutAndStageUsesIt(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	state := testState()
	profile := string(state.ConfigRaw)
	require.NoError(t, numatopology.NewEnricher(h).Enrich(t.Context(), state))
	require.Equal(t, []agent.NUMAZone{
		{ID: 0, CPUs: []int{0, 1, 4, 5}, MemoryKB: 8000},
		{ID: 1, CPUs: []int{2, 3, 6, 7}, MemoryKB: 8000},
	}, state.NodeShape.NUMA.Zones)
	require.Empty(t, state.NodeShape.Topology.RootComplexes[0].CPUAffinity, "the profile's fields stay as the source wrote them")
	require.Equal(t, profile, string(state.ConfigRaw))

	// Stage renders the enriched layout without reading the node again.
	require.NoError(t, os.Remove(h.ProcPath("meminfo")))
	require.NoError(t, os.Remove(h.SysPath("devices/system/cpu/online")))
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	require.NoError(t, sim.Stage(t.Context(), state))
	requireFileContent(t, h.RootPath("numa/sysnode/node0/cpulist"), "0-1,4-5\n")
	requireFileContent(t, h.RootPath("numa/sysnode/node1/cpulist"), "2-3,6-7\n")
}

func TestEnricherFailureLeavesTheLayoutEmpty(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	require.NoError(t, os.Remove(h.ProcPath("meminfo")))
	state := testState()
	require.Error(t, numatopology.NewEnricher(h).Enrich(t.Context(), state))
	require.Nil(t, state.NodeShape.NUMA)
}

// The agent stages only fully enriched States, so a missing layout is a bug.
func TestMissingLayoutFailsStage(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	sim, err := newTestSimulator(h, Options{Enabled: true})
	require.NoError(t, err)
	require.Error(t, sim.Stage(t.Context(), testState()))
	require.NoDirExists(t, h.RootPath("numa/sysnode"))
	require.False(t, sim.Ready())
}

func TestEnricherHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := numatopology.NewEnricher(host.New(t.TempDir())).Enrich(ctx, &agent.State{})
	require.ErrorIs(t, err, context.Canceled)
}
