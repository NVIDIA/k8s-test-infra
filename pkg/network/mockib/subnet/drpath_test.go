// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// Captured MAD header from a real `ibping <lid>` send in
// docs/demo/standalone/demo.sh (libibmad mad_build_pkt output, wire byte
// order):
//
//	mad_hdr = 01 32 01 01  00000000 0000000005335fc0 00000000 00000000
//	          BaseVer Class ClassVer Method ...
//
// Used as the gold-standard fixture for IsSMPSend: a previous implementation
// 4-byte-word-swapped this header and then read hdr[1], which is ClassVersion
// (0x01) — same value as ibClassSMI — so every vendor ping MAD was
// misclassified as SMI and bypassed the fabric routing path. Cross-pod
// ibping silently dropped every request as a result.
var ibpingVendorPingHdr = []byte{
	0x01, 0x32, 0x01, 0x01, // BaseVer, MgmtClass (vendor OpenIB ping), ClassVer, Method (Get)
	0x00, 0x00, 0x00, 0x00, // Status
	0x00, 0x00, 0x00, 0x00, 0x05, 0x33, 0x5f, 0xc0, // TransactionID
	0x00, 0x00, 0x00, 0x00, // AttributeID, Reserved
	0x00, 0x00, 0x00, 0x00, // AttributeModifier
}

// TestIsSMPSend_RejectsVendorPing locks down the routing-class fix: a real
// vendor OpenIB ping MAD (MgmtClass=0x32) must NOT be treated as an SMP, or
// else handleSend's `if !subnet.IsSMPSend(req.MAD) && s.tryFabricSend(...)`
// gate skips the cross-pod fabric path and every ibping times out with
// `recv failed: Resource temporarily unavailable`.
func TestIsSMPSend_RejectsVendorPing(t *testing.T) {
	umad := make([]byte, umadMADOffset+256)
	copy(umad[umadMADOffset:], ibpingVendorPingHdr)
	if IsSMPSend(umad) {
		t.Fatalf("vendor ping MAD (MgmtClass=0x%02x) must not be classified as SMP",
			umad[umadMADOffset+1])
	}
}

// TestIsSMPSend_AcceptsSMI / _AcceptsSMIDirect keep the positive side of the
// classifier covered alongside the vendor-ping regression test above.
func TestIsSMPSend_AcceptsSMI(t *testing.T) {
	umad := make([]byte, umadMADOffset+256)
	// MgmtClass=0x01 (SMI), ClassVer=0x01, Method=Get(0x01).
	umad[umadMADOffset+0] = 0x01
	umad[umadMADOffset+1] = ibClassSMI
	umad[umadMADOffset+2] = 0x01
	umad[umadMADOffset+3] = 0x01
	if !IsSMPSend(umad) {
		t.Fatal("SMI MAD must be classified as SMP")
	}
}

func TestIsSMPSend_AcceptsSMIDirect(t *testing.T) {
	umad := make([]byte, umadMADOffset+256)
	// MgmtClass=0x81 (SMI Direct Route), ClassVer=0x01, Method=Get(0x01).
	umad[umadMADOffset+0] = 0x01
	umad[umadMADOffset+1] = ibClassSMIDirect
	umad[umadMADOffset+2] = 0x01
	umad[umadMADOffset+3] = 0x01
	if !IsSMPSend(umad) {
		t.Fatal("SMI-direct MAD must be classified as SMP")
	}
}

// TestIsSMPSend_RejectsSA pins the SA(0x03) case so the future habit of
// "we'll just byte-swap and look at hdr[1]" can't quietly come back.
func TestIsSMPSend_RejectsSA(t *testing.T) {
	umad := make([]byte, umadMADOffset+256)
	umad[umadMADOffset+0] = 0x01
	umad[umadMADOffset+1] = 0x03 // SA
	umad[umadMADOffset+2] = 0x02
	umad[umadMADOffset+3] = 0x01
	if IsSMPSend(umad) {
		t.Fatal("SA MAD must not be classified as SMP")
	}
}

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
