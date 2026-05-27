// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabric

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

func TestGraph_FullMeshNeighbors(t *testing.T) {
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
	}
	peers := map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
		"a088:c203:00ab:0003": {LID: 0x103, CAName: "mlx5_1", PodIP: "10.0.0.2"},
	}
	g := Build(local, peers)
	if len(g.Ports()) != 3 {
		t.Fatalf("ports: got %d want 3", len(g.Ports()))
	}
	remote, ok := g.ByLID(0x102)
	if !ok || remote.PodIP != "10.0.0.2" {
		t.Fatalf("ByLID: %+v ok=%v", remote, ok)
	}
	p := g.Ports()[0]
	nb, ok := g.InboundNeighborFor(remote)
	if !ok || nb.PortGUID != p.PortGUID {
		t.Fatalf("inbound for remote: %+v ok=%v", nb, ok)
	}
}
