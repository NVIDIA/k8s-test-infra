// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/stretchr/testify/require"
)

// writeMADHeader writes a wire-format SMP GET header (IBA §13.4) into mad:
// BaseVer=1, MgmtClass=class, ClassVer=1, Method=Get, AttrID (BE16 @16).
// This matches the byte order libibumad delivers to the daemon, which is what
// TrySynthesize parses (see IsSMPSend for the same convention).
func writeMADHeader(mad []byte, class byte, attr uint16) {
	mad[0] = 0x01 // BaseVersion
	mad[1] = class
	mad[2] = 0x01 // ClassVersion
	mad[ibMADMethodOff] = 0x01
	binary.BigEndian.PutUint16(mad[ibMADAttrIDOff:ibMADAttrIDOff+2], attr)
}

func TestTrySynthesize_PortInfo(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]

	writeMADHeader(mad, ibClassSMI, ibAttrPortInfo)

	binary.BigEndian.PutUint16(send[28:30], 0x101)
	resp, ok := TrySynthesize(send, g, "mlx5_0")
	require.True(t, ok, "expected synthesize")
	rm := resp[umadMADOffset:]
	require.NotEqual(t, byte(0), rm[ibMADMethodOff]&0x80, "response method bit not set")
}

func TestTrySynthesize_SelfResolveLID0(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", NodeGUID: "a088:c203:00ab:0000", LID: 0x101, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]

	writeMADHeader(mad, ibClassSMI, ibAttrNodeInfo)

	// dlid 0 self-resolve
	send[28] = 0
	send[29] = 0
	_, ok := TrySynthesize(send, g, "mlx5_0")
	require.True(t, ok, "expected synthesize for lid 0 self resolve")
}

func TestTrySynthesize_PortInfoMod0PhysLinkUp(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x0dc0, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+256)
	mad := send[umadMADOffset:]
	writeMADHeader(mad, ibClassSMIDirect, ibAttrPortInfo)
	SetField(mad, ibDRHopCntBit, 8, 0)
	binary.BigEndian.PutUint16(send[28:30], 0xffff)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	require.True(t, ok, "expected synthesize")
	pl := resp[umadMADOffset+ibSMPDataOff:]
	require.Equal(t, uint32(portPhysStateLinkUp), GetFieldSpec(pl, 264, 4), "phys")
	require.Equal(t, uint32(0x0dc0), GetFieldSpec(pl, 128, 16), "lid")
}

func TestTrySynthesize_ShortSendBuffer(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x0f90, CAName: "mlx5_0"},
	}, nil)
	// libibmad often sends header + ~64 MAD bytes, not the full 256-byte MAD.
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]
	writeMADHeader(mad, ibClassSMIDirect, ibAttrPortInfo)
	SetField(mad, ibDRHopCntBit, 8, 0)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	require.True(t, ok, "expected synthesize")
	require.GreaterOrEqual(t, len(resp), minUmadLen, "resp len %d want >= %d", len(resp), minUmadLen)
	pl := resp[umadMADOffset+ibSMPDataOff:]
	require.Equal(t, uint32(0x0f90), GetFieldSpec(pl, 128, 16), "lid")
	require.Equal(t, uint32(portPhysStateLinkUp), GetFieldSpec(pl, 264, 4), "phys")
}

// TestFillPortInfo_WireBytes pins the on-wire byte layout of a synthesized
// PortInfo SMP, reading the payload as raw big-endian bytes at the IB-spec
// wire offsets a real libibmad consumer (iblinkinfo) parses — deliberately
// NOT via GetFieldSpec, which would re-apply the same BITSOFFS / 3^idx
// transform fillPortInfo used to write and mask a symmetric offset regression
// (e.g. a Bitsoffs or SetField change that breaks set+get together). The
// per-field offsets below are the libibmad fields.c byte positions: a field at
// spec bit offset o, byte width w lands at wire byte o/8 in network order.
func TestFillPortInfo_WireBytes(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	fillPortInfo(mad, fabricPort(0x0dc0), 1)
	pl := mad[ibSMPDataOff:]

	// GidPrefix (spec bit 64 -> byte 8, BE64).
	require.Equal(t, uint64(defaultGidPrefix), binary.BigEndian.Uint64(pl[8:16]), "GidPrefix @byte8")
	// LID (spec bit 128 -> byte 16, BE16).
	require.Equal(t, uint16(0x0dc0), binary.BigEndian.Uint16(pl[16:18]), "LID @byte16")
	// MasterSMLID (spec bit 144 -> byte 18, BE16).
	require.Equal(t, uint16(defaultSMLID), binary.BigEndian.Uint16(pl[18:20]), "SMLID @byte18")
	// PortState occupies the low nibble of wire byte 32; PortPhysState the
	// high nibble of wire byte 33 (libibmad PORT_INFO field defs).
	require.Equal(t, byte(portStateActive), pl[32]&0x0f, "PortState @byte32 low nibble")
	require.Equal(t, byte(portPhysStateLinkUp), pl[33]>>4, "PortPhysState @byte33 high nibble")
}

// TestFillNodeInfo_WireBytes pins the on-wire byte layout of a synthesized
// NodeInfo SMP via raw big-endian reads at the libibmad fields.c byte offsets,
// independent of GetFieldSpec (see TestFillPortInfo_WireBytes rationale).
func TestFillNodeInfo_WireBytes(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	p := fabricPort(0x101)
	fillNodeInfo(mad, p, 0)
	pl := mad[ibSMPDataOff:]

	// NodeType (spec bit 16 -> byte 2) and NumPorts (spec bit 24 -> byte 3).
	require.Equal(t, byte(nodeTypeCA), pl[2], "NodeType @byte2")
	require.Equal(t, byte(1), pl[3], "NumPorts @byte3")
	// SystemGuid @byte4 and NodeGuid @byte12 are both p.NodeGUID; PortGuid
	// @byte20 is p.PortGUID (all 8-byte BE, written by putGUID64).
	nodeGUID := []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x00, 0x00}
	portGUID := []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x00, 0x01}
	require.Equal(t, nodeGUID, pl[4:12], "SystemGuid @byte4")
	require.Equal(t, nodeGUID, pl[12:20], "NodeGuid @byte12")
	require.Equal(t, portGUID, pl[20:28], "PortGuid @byte20")
	// LocalPort (spec bit 288 -> byte 36); attrMod 0 resolves to port 1.
	require.Equal(t, byte(1), pl[36], "LocalPort @byte36")
}

// TestFillSMInfo_WireBytes pins the on-wire byte layout of a synthesized SMInfo
// SMP via raw big-endian reads at the libibmad fields.c byte offsets, NOT via
// GetFieldSpec (see TestFillPortInfo_WireBytes rationale). Byte 20 carries
// Priority in the high nibble and SMState in the low nibble.
func TestFillSMInfo_WireBytes(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	fillSMInfo(mad, fabricPort(0x101))
	pl := mad[ibSMPDataOff:]

	// GUID (spec bit 0 -> byte 0, BE64, plain via putGUID64).
	require.Equal(t, []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x00, 0x01}, pl[0:8], "SM GUID @byte0")
	// SM_Key (spec bit 64 -> byte 8, BE64).
	require.Equal(t, uint64(defaultSMKey), binary.BigEndian.Uint64(pl[8:16]), "SM_Key @byte8")
	// ActCount (spec bit 128 -> byte 16, BE32).
	require.Equal(t, uint32(smActCount), binary.BigEndian.Uint32(pl[16:20]), "ActCount @byte16")
	// Priority high nibble + SMState low nibble of byte 20.
	require.Equal(t, byte(defaultSMPriority), pl[20]>>4, "Priority @byte20 high nibble")
	require.Equal(t, byte(smStateMaster), pl[20]&0x0f, "SMState @byte20 low nibble")
}

func TestTrySynthesize_SMInfo(t *testing.T) {
	// sminfo LID-routes SMInfo to the advertised SM LID (1), which is not a real
	// port; the daemon must still answer from the elected master (lowest GUID).
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0005", LID: 0x105, CAName: "mlx5_0"},
	}, map[string]registry.Peer{
		"a088:c203:00ab:0002": {LID: 0x102, CAName: "mlx5_0", PodIP: "10.0.0.2"},
	})
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]
	writeMADHeader(mad, ibClassSMI, ibAttrSMInfo)
	binary.BigEndian.PutUint16(send[28:30], defaultSMLID)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	require.True(t, ok, "expected SMInfo synthesize")
	require.GreaterOrEqual(t, len(resp), minUmadLen, "resp padded to full umad frame")
	rm := resp[umadMADOffset:]
	require.NotEqual(t, byte(0), rm[ibMADMethodOff]&0x80, "response method bit not set")

	pl := resp[umadMADOffset+ibSMPDataOff:]
	// Master SM GUID = lowest PortGUID across the merged graph (peer 0002).
	require.Equal(t, []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x00, 0x02}, pl[0:8], "SM GUID = lowest PortGUID")
	require.Equal(t, uint32(smStateMaster), GetFieldSpec(pl, 164, 4), "SMState MASTER")
}

func TestTrySynthesize_SMInfoSubnSetIgnored(t *testing.T) {
	g := fabric.Build([]protocol.PortAdvert{
		{PortGUID: "a088:c203:00ab:0001", LID: 0x101, CAName: "mlx5_0"},
	}, nil)
	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]
	writeMADHeader(mad, ibClassSMI, ibAttrSMInfo)
	mad[ibMADMethodOff] = 0x02 // SubnSet (sminfo -p/-s/-a); mock has no mutable SM
	_, ok := TrySynthesize(send, g, "mlx5_0")
	require.False(t, ok, "SubnSet(SMInfo) must not be synthesized")
}

func TestTrySynthesize_SMInfoEmptyGraph(t *testing.T) {
	send := make([]byte, umadMADOffset+64)
	writeMADHeader(send[umadMADOffset:], ibClassSMI, ibAttrSMInfo)
	_, ok := TrySynthesize(send, fabric.Build(nil, nil), "mlx5_0")
	require.False(t, ok, "no master SM without any ports")
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
	writeMADHeader(mad, ibClassSMIDirect, ibAttrNodeInfo)
	SetField(mad, ibDRHopCntBit, 8, 1)
	mad[ibDRPathByteOff] = 1
	binary.BigEndian.PutUint16(send[28:30], 0xffff)

	resp, ok := TrySynthesize(send, g, "mlx5_0")
	require.True(t, ok, "expected synthesize for DR hop")
	pl := resp[umadMADOffset+ibSMPDataOff:]
	// PortGuid @ byte 20 should be the peer port (0002).
	want := []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x00, 0x02}
	require.Equal(t, want, pl[20:28], "peer port guid")
}
