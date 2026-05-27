// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package subnet synthesizes subnet management MAD responses for iblinkinfo.
package subnet

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
)

var debugSMP = os.Getenv("MOCK_IB_DEBUG_SMP") == "1"

func smpLogf(format string, args ...interface{}) {
	if debugSMP {
		log.Print("mock-ib smp: ", fmt.Sprintf(format, args...))
	}
}

const (
	umadMADOffset  = 56
	// Full legacy umad buffer: 56-byte header + 256-byte MAD (libibumad umad_size + MAD).
	minUmadLen = umadMADOffset + 256
	ibMADMethodOff = 3
	// MAD header AttrID lives at bytes 18..19 (see libibmad: IB_MAD_ATTRID_F / BE_OFFS(144,16)).
	ibMADAttrIDOff = 18

	// SMP payload is copied from MAD+64 (see libibmad: IB_SMP_DATA_OFFS).
	ibSMPDataOff = 64

	ibAttrNodeDesc = 0x0010
	ibAttrNodeInfo = 0x0011
	ibAttrPortInfo = 0x0015
	ibClassSMI       = 0x01
	ibClassSMIDirect = 0x81

	// PortInfo / NodeInfo values understood by libibmad / iblinkinfo.
	portStateActive     = 4 // PORT_ACTIVE
	portPhysStateLinkUp = 5
	linkWidth4X         = 2
	linkSpeed10G        = 4
	nodeTypeCA          = 1
	defaultSMLID        = 1
	defaultGidPrefix    = 0xfe80000000000000
)

// SMPGetHeader carries decoded subnet GET header fields from a UMAD buffer.
type SMPGetHeader struct {
	AttrID  uint16
	AttrMod uint32
}

// DecodeSMPGet parses a subnet management GET from umad (best-effort).
func DecodeSMPGet(umad []byte) (SMPGetHeader, bool) {
	var h SMPGetHeader
	if len(umad) < umadMADOffset+24 {
		return h, false
	}
	mad := umad[umadMADOffset:]
	hdr, ok := normalizeMADHeader(mad)
	if !ok {
		return h, false
	}
	if hdr[1] != ibClassSMI && hdr[1] != ibClassSMIDirect {
		return h, false
	}
	if hdr[ibMADMethodOff]&0x7f != 0x01 {
		return h, false
	}
	h.AttrID = decodeAttrID(hdr)
	h.AttrMod = binary.BigEndian.Uint32(hdr[20:24])
	return h, true
}

// TrySynthesize returns a RECV umad buffer for subnet GETs, or (nil, false).
// localCA is the umad-opened HCA (e.g. mlx5_0) used to resolve self-queries (dlid 0).
func TrySynthesize(sendMad []byte, g *fabric.Graph, localCA string) ([]byte, bool) {
	if g == nil || len(sendMad) < umadMADOffset+16 {
		return nil, false
	}
	mad := sendMad[umadMADOffset:]
	if len(mad) < 12 {
		return nil, false
	}

	hdr, ok := normalizeMADHeader(mad)
	if !ok {
		return nil, false
	}

	cls := hdr[1]
	if cls != ibClassSMI && cls != ibClassSMIDirect {
		return nil, false
	}
	method := hdr[ibMADMethodOff] & 0x7f
	if method != 0x01 { // GET
		return nil, false
	}
	attrID := decodeAttrID(hdr)
	attrMod := binary.BigEndian.Uint32(hdr[20:24])

	lid, ok := destLID(sendMad)
	if !ok {
		return nil, false
	}
	target, ok := resolveTarget(g, mad, lid, localCA)
	if !ok {
		smpLogf("attr=0x%04x mod=0x%x lid=0x%x localCA=%s hopCnt=%d hops=%02x:%02x:%02x:%02x NO_TARGET",
			attrID, attrMod, lid, localCA, drHopCnt(mad), drPathByte(mad, 1), drPathByte(mad, 2), drPathByte(mad, 3), drPathByte(mad, 4))
		return nil, false
	}
	smpLogf("attr=0x%04x mod=0x%x lid=0x%x localCA=%s hopCnt=%d hops=%02x:%02x:%02x:%02x -> %s/%s lid=0x%x local=%t podIP=%s",
		attrID, attrMod, lid, localCA, drHopCnt(mad), drPathByte(mad, 1), drPathByte(mad, 2), drPathByte(mad, 3), drPathByte(mad, 4),
		target.CAName, target.PortGUID, target.LID, target.Local, target.PodIP)
	out := make([]byte, len(sendMad))
	if len(sendMad) < minUmadLen {
		out = make([]byte, minUmadLen)
	}
	copy(out, sendMad)
	if len(out) >= 8 {
		// ib_user_mad.status (offset 4); non-zero makes libibmad mad_rpc retry/discard.
		binary.LittleEndian.PutUint32(out[4:8], 0)
	}
	respMAD := out[umadMADOffset:]
	respMAD[ibMADMethodOff] = method | 0x80
	switch attrID {
	case ibAttrNodeDesc:
		fillNodeDesc(respMAD, target)
		return out, true
	case ibAttrNodeInfo:
		fillNodeInfo(respMAD, target, attrMod)
		return out, true
	case ibAttrPortInfo:
		fillPortInfo(respMAD, target, attrMod)
		return out, true
	default:
		return nil, false
	}
}

func normalizeMADHeader(mad []byte) ([24]byte, bool) {
	var hdr [24]byte
	if len(mad) < len(hdr) {
		return hdr, false
	}
	for w := 0; w < len(hdr); w += 4 {
		hdr[w+0] = mad[w+3]
		hdr[w+1] = mad[w+2]
		hdr[w+2] = mad[w+1]
		hdr[w+3] = mad[w+0]
	}
	return hdr, true
}

func decodeAttrID(hdr [24]byte) uint16 {
	attrID := binary.BigEndian.Uint16(hdr[ibMADAttrIDOff : ibMADAttrIDOff+2])
	return (attrID >> 8) | (attrID << 8)
}

func fillNodeDesc(mad []byte, p fabric.Port) {
	if len(mad) < ibSMPDataOff+64 {
		return
	}
	pl := mad[ibSMPDataOff:]
	copy(pl[:64], []byte("mock-ib "+p.CAName))
}

func destLID(umad []byte) (uint16, bool) {
	if len(umad) < 30 {
		return 0, false
	}
	return binary.BigEndian.Uint16(umad[28:30]), true
}

func fillNodeInfo(mad []byte, p fabric.Port, attrMod uint32) {
	if len(mad) < ibSMPDataOff+64 {
		return
	}
	pl := mad[ibSMPDataOff:]
	_ = attrMod

	SetFieldSpec(pl, 16, 8, nodeTypeCA)
	SetFieldSpec(pl, 24, 8, 1) // NumPorts
	putGUID64(pl, 4, p.NodeGUID)  // SystemGuid @ bit 32
	putGUID64(pl, 12, p.NodeGUID) // NodeGuid @ bit 96
	putGUID64(pl, 20, p.PortGUID) // PortGuid @ bit 160

	portNum := attrMod
	if portNum == 0 {
		portNum = 1 // ib_resolve_self and CA port-1 discovery use mod 0
	}
	SetFieldSpec(pl, 288, 8, portNum) // LocalPort
}

func fillPortInfo(mad []byte, p fabric.Port, attrMod uint32) {
	if len(mad) < ibSMPDataOff+64 {
		return
	}
	pl := mad[ibSMPDataOff:]
	for i := range pl {
		pl[i] = 0
	}

	portNum := attrMod
	if portNum == 0 {
		portNum = 1 // ib_resolve_self and CA port-1 discovery use mod 0
	}

	// libibmad fields.c PORT_INFO layout; use BITSOFFS via SetFieldSpec for SMP fields.
	SetField64(pl, 64, defaultGidPrefix) // GidPrefix {64,64} byte-aligned
	SetFieldSpec(pl, 128, 16, uint32(p.LID))
	SetFieldSpec(pl, 144, 16, defaultSMLID)
	SetFieldSpec(pl, 224, 8, portNum) // LocalPort
	SetFieldSpec(pl, 232, 8, linkWidth4X)
	SetFieldSpec(pl, 240, 8, linkWidth4X)
	SetFieldSpec(pl, 248, 8, linkWidth4X)
	SetFieldSpec(pl, 256, 4, linkSpeed10G)
	SetFieldSpec(pl, 260, 4, portStateActive)
	SetFieldSpec(pl, 264, 4, portPhysStateLinkUp)
	SetFieldSpec(pl, 277, 3, 0) // LMC
	SetFieldSpec(pl, 280, 4, linkSpeed10G)
	SetFieldSpec(pl, 284, 4, linkSpeed10G)
}

func putGUID64(mad []byte, byteOff int, guidColon string) {
	if len(mad) < byteOff+8 {
		return
	}
	var parts [4]uint16
	n, _ := parseGUID(guidColon)
	copy(parts[:], n[:])
	for i := 0; i < 4; i++ {
		binary.BigEndian.PutUint16(mad[byteOff+i*2:], parts[i])
	}
}

func parseGUID(s string) ([4]uint16, bool) {
	var out [4]uint16
	var hex []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			hex = append(hex, c)
		}
	}
	if len(hex) < 16 {
		return out, false
	}
	hex = hex[len(hex)-16:]
	for i := 0; i < 4; i++ {
		var v uint16
		for j := 0; j < 4; j++ {
			v = v<<4 | hexDigit(hex[i*4+j])
		}
		out[i] = v
	}
	return out, true
}

func hexDigit(c byte) uint16 {
	switch {
	case c >= '0' && c <= '9':
		return uint16(c - '0')
	case c >= 'a' && c <= 'f':
		return uint16(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return uint16(c - 'A' + 10)
	default:
		return 0
	}
}
