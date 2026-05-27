// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

func TestTrySynthesize_PortInfo(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]

	// Build a "normalized" MAD header, then scramble it to match libibmad packing.
	hdr := make([]byte, 24)
	hdr[1] = ibClassSMI
	hdr[ibMADMethodOff] = 0x01
	// Match synthesize.go normalization + 16-bit swap.
	attr := uint16(ibAttrPortInfo)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[ibMADAttrIDOff:ibMADAttrIDOff+2], attr)
	for w := 0; w < len(hdr); w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}

	binary.BigEndian.PutUint16(send[28:30], 0x101)
	resp, ok := TrySynthesize(send, g, "mlx5_0")
	if !ok {
		t.Fatal("expected synthesize")
	}
	rm := resp[umadMADOffset:]
	if rm[ibMADMethodOff]&0x80 == 0 {
		t.Fatal("response method bit not set")
	}
}

func TestTrySynthesize_SelfResolveLID0(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", NodeGUID: "a088:c203:00ab:0000", LID: 0x101, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]

	hdr := make([]byte, 24)
	hdr[1] = ibClassSMI
	hdr[ibMADMethodOff] = 0x01
	attr := uint16(ibAttrNodeInfo)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[ibMADAttrIDOff:ibMADAttrIDOff+2], attr)
	for w := 0; w < len(hdr); w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}

	// dlid 0 self-resolve
	send[28] = 0
	send[29] = 0
	_, ok := TrySynthesize(send, g, "mlx5_0")
	if !ok {
		t.Fatal("expected synthesize for lid 0 self resolve")
	}
}

func TestTrySynthesize_PortInfoMod0PhysLinkUp(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x0dc0, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+256)
	mad := send[umadMADOffset:]
	hdr := make([]byte, 24)
	hdr[1] = ibClassSMIDirect
	hdr[ibMADMethodOff] = 0x01
	attr := uint16(ibAttrPortInfo)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[ibMADAttrIDOff:ibMADAttrIDOff+2], attr)
	for w := 0; w < 24; w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}
	SetField(mad, ibDRHopCntBit, 8, 0)
	binary.BigEndian.PutUint16(send[28:30], 0xffff)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	if !ok {
		t.Fatal("expected synthesize")
	}
	pl := resp[umadMADOffset+ibSMPDataOff:]
	if got := GetFieldSpec(pl, 264, 4); got != portPhysStateLinkUp {
		t.Fatalf("phys: got %d want %d", got, portPhysStateLinkUp)
	}
	if got := GetFieldSpec(pl, 128, 16); got != 0x0dc0 {
		t.Fatalf("lid: got %#x want 0x0dc0", got)
	}
}

func TestTrySynthesize_ShortSendBuffer(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x0f90, CAName: "mlx5_0"},
	}, nil)
	// libibmad often sends header + ~64 MAD bytes, not the full 256-byte MAD.
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]
	hdr := make([]byte, 24)
	hdr[1] = ibClassSMIDirect
	hdr[ibMADMethodOff] = 0x01
	attr := uint16(ibAttrPortInfo)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[ibMADAttrIDOff:ibMADAttrIDOff+2], attr)
	for w := 0; w < 24; w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}
	SetField(mad, ibDRHopCntBit, 8, 0)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	if !ok {
		t.Fatal("expected synthesize")
	}
	if len(resp) < minUmadLen {
		t.Fatalf("resp len %d want >= %d", len(resp), minUmadLen)
	}
	pl := resp[umadMADOffset+ibSMPDataOff:]
	if got := GetFieldSpec(pl, 128, 16); got != 0x0f90 {
		t.Fatalf("lid: got %#x want 0x0f90", got)
	}
	if got := GetFieldSpec(pl, 264, 4); got != portPhysStateLinkUp {
		t.Fatalf("phys: got %d want %d", got, portPhysStateLinkUp)
	}
}

func TestTrySynthesize_DROneHopNodeInfo(t *testing.T) {
	local := []protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", NodeGUID: "a088:c203:00ab:0000", LID: 0x101, CAName: "mlx5_0"},
	}
	peers := map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
	}
	g := fabric.Build(local, peers)

	send := make([]byte, umadMADOffset+256)
	mad := send[umadMADOffset:]
	hdr := make([]byte, 24)
	hdr[1] = ibClassSMIDirect
	hdr[ibMADMethodOff] = 0x01
	attr := uint16(ibAttrNodeInfo)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[ibMADAttrIDOff:ibMADAttrIDOff+2], attr)
	for w := 0; w < len(hdr); w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}
	SetField(mad, ibDRHopCntBit, 8, 1)
	mad[ibDRPathByteOff] = 1
	binary.BigEndian.PutUint16(send[28:30], 0xffff)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	if !ok {
		t.Fatal("expected synthesize for DR hop")
	}
	pl := resp[umadMADOffset+ibSMPDataOff:]
	// PortGuid @ byte 20 should be the peer port (0002).
	want := []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x00, 0x02}
	if string(pl[20:28]) != string(want) {
		t.Fatalf("peer port guid: got %x want %x", pl[20:28], want)
	}
}
