// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"log"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/subnet"
)

// SMP GETs to a peer LID must be answered by subnet synthesis, not fabric ping + loopback echo.
func TestTryFabricSend_SkipsSubnetMAD(t *testing.T) {
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0", Port: 1},
	}
	srv := &Server{
		cfg:        Config{Fabric: true},
		localPorts: local,
		registry:   registry.New(),
		podIP:      "10.0.0.1",
		loopback:   NewLoopback(local),
		log:        log.Default(),
		handles:    make(map[int]*portHandle),
	}
	srv.registry.Register("a088:c203:00ab:0002", registry.Peer{
		LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2",
	})
	srv.rebuildGraph()

	send := make([]byte, umadMADOffset+256)
	mad := send[umadMADOffset:]
	hdr := make([]byte, 24)
	hdr[1] = 0x81
	hdr[3] = 0x01
	attr := uint16(0x0015)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[18:20], attr)
	for w := 0; w < 24; w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}
	binary.BigEndian.PutUint16(send[28:30], 0x102)

	h := &portHandle{caName: "mlx5_0", port: 1}
	if !subnet.IsSMPSend(send) {
		t.Fatal("test MAD should be SMP")
	}
	if srv.tryFabricSend(h, send) {
		t.Fatal("tryFabricSend must not handle subnet SMP MADs")
	}

	srv.graphMu.RLock()
	g := srv.graph
	srv.graphMu.RUnlock()
	resp, ok := subnet.TrySynthesize(send, g, h.caName)
	if !ok {
		t.Fatal("TrySynthesize: expected peer PORT_INFO")
	}
	pl := resp[umadMADOffset+64:]
	if got := subnet.GetFieldSpec(pl, 264, 4); got != 5 {
		t.Fatalf("peer phys: got %d want 5 (link up)", got)
	}
	if got := subnet.GetFieldSpec(pl, 128, 16); got != 0x102 {
		t.Fatalf("peer lid: got %#x want 0x102", got)
	}
	_ = fabric.Port{}
}
