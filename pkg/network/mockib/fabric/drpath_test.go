// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabric

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

func TestPeerAtOutbound_FirstHopRemote(t *testing.T) {
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
		{PortGUID: "a088:c203:00ab:0011", LID: 0x111, CAName: "mlx5_1"},
	}
	peers := map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
		"a088:c203:00ab:0012": {LID: 0x112, CAName: "mlx5_1", PodIP: "10.0.0.3"},
	}
	g := Build(local, peers)
	from, ok := g.ByCAName("mlx5_0")
	if !ok {
		t.Fatal("local port")
	}
	peer, ok := g.PeerAtOutbound(from, 1, 0)
	if !ok || peer.Local || peer.PortGUID != "a088:c203:00ab:0002" {
		t.Fatalf("peer: %+v ok=%v", peer, ok)
	}
}

func TestPeerAtOutbound_SecondHopDifferentPod(t *testing.T) {
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
	}
	peers := map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
		"a088:c203:00ab:0003": {LID: 0x103, CAName: "mlx5_0", PodIP: "10.0.0.3"},
	}
	g := Build(local, peers)
	from, _ := g.ByCAName("mlx5_0")
	first, ok := g.PeerAtOutbound(from, 1, 0)
	if !ok {
		t.Fatal("first hop")
	}
	second, ok := g.PeerAtOutbound(first, 1, 1)
	if !ok {
		t.Fatal("second hop")
	}
	if second.PortGUID == first.PortGUID {
		t.Fatalf("second hop must be a different port, got %s", second.PortGUID)
	}
	if second.PodIP == first.PodIP {
		t.Fatalf("second hop must be a different pod")
	}
}
