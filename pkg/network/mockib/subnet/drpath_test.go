// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

func TestResolveTarget_DROneHopPeerNotLocal(t *testing.T) {
	g := fabric.Build(
		[]protocol.PortAdvert{
			{PortGUID: "a088:c203:00ab:5601", NodeGUID: "a088:c203:00ab:5600", LID: 0x660, CAName: "mlx5_0"},
		},
		map[string]registry.Peer{
			"a088:c203:00ab:2001": {LID: 0x661, CAName: "mlx5_0", PodIP: "10.0.0.2"},
		},
	)
	mad := make([]byte, 256)
	SetField(mad, ibDRHopCntBit, 8, 1)
	// path[0] is reserved per IB spec 14.2.1.2; first outbound port is path[1].
	mad[ibDRPathByteOff+1] = 1

	p, ok := resolveTarget(g, mad, 0xffff, "mlx5_0")
	if !ok {
		t.Fatal("expected peer resolve")
	}
	if p.PortGUID == "a088:c203:00ab:5601" {
		t.Fatalf("DR hop 1 should target peer, got local port GUID %s", p.PortGUID)
	}
	if p.PortGUID != "a088:c203:00ab:2001" {
		t.Fatalf("peer GUID: got %s", p.PortGUID)
	}
}

func TestResolveTarget_TwoHopDifferentPeer(t *testing.T) {
	g := fabric.Build(
		[]protocol.PortAdvert{
			{PortGUID: "a088:c203:00ab:5601", NodeGUID: "a088:c203:00ab:5600", LID: 0x660, CAName: "mlx5_0"},
		},
		map[string]registry.Peer{
			"a088:c203:00ab:2001": {LID: 0x661, CAName: "mlx5_0", PodIP: "10.0.0.2"},
			"a088:c203:00ab:3001": {LID: 0x662, CAName: "mlx5_0", PodIP: "10.0.0.3"},
		},
	)
	mad := make([]byte, 256)
	SetField(mad, ibDRHopCntBit, 8, 2)
	// path[0] is reserved; outbound ports at path[1], path[2].
	mad[ibDRPathByteOff+1] = 1
	mad[ibDRPathByteOff+2] = 1

	p, ok := resolveTarget(g, mad, 0xffff, "mlx5_0")
	if !ok {
		t.Fatal("expected two-hop resolve")
	}
	if p.PortGUID == "a088:c203:00ab:5601" {
		t.Fatal("two-hop must not resolve to local port")
	}
	if p.PodIP == "10.0.0.2" {
		// second hop should land on the other peer pod when two remotes exist
		t.Fatalf("expected second peer pod, got %s", p.PodIP)
	}
}
