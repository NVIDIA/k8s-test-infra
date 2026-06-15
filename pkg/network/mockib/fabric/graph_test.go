// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabric

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/stretchr/testify/require"
)

func TestGraph_BuildAndLookup(t *testing.T) {
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
	}
	peers := map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
		"a088:c203:00ab:0003": {LID: 0x103, CAName: "mlx5_1", PodIP: "10.0.0.2"},
	}
	g := Build(local, peers)
	require.Len(t, g.Ports(), 3, "ports")
	remote, ok := g.ByLID(0x102)
	require.True(t, ok, "ByLID: %+v ok=%v", remote, ok)
	require.Equal(t, "10.0.0.2", remote.PodIP, "ByLID: %+v ok=%v", remote, ok)
}

func TestGraph_MasterSM(t *testing.T) {
	// Local and peer ports given out of GUID order; MasterSM must elect the
	// lowest PortGUID across the merged graph so every pod agrees on one SM.
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0005", LID: 0x105, CAName: "mlx5_1"},
	}
	peers := map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
		"a088:c203:00ab:0009": {LID: 0x109, CAName: "mlx5_1", PodIP: "10.0.0.3"},
	}
	g := Build(local, peers)
	sm, ok := g.MasterSM()
	require.True(t, ok, "MasterSM ok")
	require.Equal(t, "a088:c203:00ab:0002", sm.PortGUID, "lowest PortGUID elected")

	_, ok = Build(nil, nil).MasterSM()
	require.False(t, ok, "empty graph has no master SM")
}
