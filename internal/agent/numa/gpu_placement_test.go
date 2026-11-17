// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"slices"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/numatopology"
	"github.com/stretchr/testify/require"
)

func TestResolvedGPUPlacementMatchesActivePCITopology(t *testing.T) {
	t.Parallel()
	state := testState()
	state.Devices = []agent.DeviceSpec{
		{PCIBusID: "0000:87:00.0"}, {PCIBusID: "0000:0F:00.0"},
		{PCIBusID: "0000:07:00.0"}, {PCIBusID: "0000:ff:00.0"},
	}
	state.Switches = []agent.SwitchSpec{{PCIBusID: "0000:01:00.0"}}
	state.NodeShape.Topology.RootComplexes[0].DeviceBDFs = []string{
		"0000:07:00.0", "0000:0F:00.0", "0000:01:00.0", "0000:ee:00.0",
	}
	layout, err := numatopology.Resolve(testHost(t), state)
	require.NoError(t, err)
	require.Equal(t, []agent.NUMAGPU{
		{PCIBusID: "0000:07:00.0", NUMANode: 0},
		{PCIBusID: "0000:0f:00.0", NUMANode: 0},
		{PCIBusID: "0000:87:00.0", NUMANode: 1},
		{PCIBusID: "0000:ff:00.0", NUMANode: -1},
	}, layout.GPUs, "exclude switches and inactive devices, normalize addresses, and preserve unknown locality")
}

func TestGPUDeclarationOrderKeepsAppliedLayout(t *testing.T) {
	t.Parallel()
	sim, state, calls := reconfigurationFixture(t, nil)
	state.Devices = []agent.DeviceSpec{
		{PCIBusID: "0000:07:00.0"}, {PCIBusID: "0000:0f:00.0"}, {PCIBusID: "0000:87:00.0"},
	}
	state.NodeShape.Topology.RootComplexes[0].DeviceBDFs = []string{"0000:07:00.0", "0000:0F:00.0"}
	enricher := numatopology.NewEnricher(sim.host)
	require.NoError(t, enricher.Enrich(t.Context(), state))
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	key := sim.desired
	*calls = nil
	slices.Reverse(state.Devices)
	slices.Reverse(state.NodeShape.Topology.RootComplexes)
	for i := range state.NodeShape.Topology.RootComplexes {
		slices.Reverse(state.NodeShape.Topology.RootComplexes[i].DeviceBDFs)
	}
	require.NoError(t, enricher.Enrich(t.Context(), state))
	require.NoError(t, sim.PrepareStage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Apply(t.Context(), state))
	require.Equal(t, key, sim.desired)
	require.Empty(t, *calls, "reordering a profile must not interrupt allocations")
}
