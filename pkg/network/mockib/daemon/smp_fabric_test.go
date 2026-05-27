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

// SMP GETs to a peer LID must be answered by subnet synthesis, not fabric ping
// + loopback echo. This was the test that initially shipped against the legacy
// IsSMPSend implementation (4-byte-word-swap of the MAD header, then read of
// hdr[1] which is ClassVersion not MgmtClass). The test passed only because it
// constructed the MAD using the same wrong swap and `hdr[1]=0x81`. Real MADs
// from libibmad arrive in wire byte order, so we now build a real wire-format
// SMI-direct PORT_INFO request and assert:
//
//  1. IsSMPSend returns true (so handleSend's gate
//     `!subnet.IsSMPSend && tryFabricSend` correctly skips the fabric path),
//  2. tryFabricSend self-rejects the MAD as defense-in-depth (the early
//     `if subnet.IsSMPSend(sendMad) { return false }` guard added together
//     with the IsSMPSend fix), and
//  3. TrySynthesize still produces a well-formed peer PORT_INFO response.
//
// The companion subnet-package test `TestIsSMPSend_RejectsVendorPing` covers
// the negative side that motivated the fix (vendor OpenIB ping, MgmtClass
// 0x32, ClassVersion 0x01 — used to be misclassified as SMI and silently
// dropped by both fabric and loopback branches of handleSend).
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
	// MAD wire layout (IBA §13.4): BaseVer | MgmtClass | ClassVer | Method | ...
	// followed by Status, TID, AttrID (BE16 @16), Reserved, AttrMod (BE32 @20).
	mad[0] = 0x01  // BaseVersion
	mad[1] = 0x81  // MgmtClass = SMI Direct Route
	mad[2] = 0x01  // ClassVersion
	mad[3] = 0x01  // Method = Get
	mad[16] = 0x00 // AttributeID hi
	mad[17] = 0x15 // AttributeID lo = PORT_INFO (0x0015)
	binary.BigEndian.PutUint16(send[28:30], 0x102)

	h := &portHandle{caName: "mlx5_0", port: 1}
	if !subnet.IsSMPSend(send) {
		t.Fatalf("wire-format SMI-direct MAD (mad[1]=0x%02x) must be SMP", mad[1])
	}
	if srv.tryFabricSend(h, send) {
		t.Fatal("tryFabricSend must reject subnet SMP MADs (defense-in-depth)")
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
