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
	umadMADOffset = 56
	// Full legacy umad buffer: 56-byte header + 256-byte MAD (libibumad umad_size + MAD).
	minUmadLen     = umadMADOffset + 256
	ibMADMethodOff = 3
	// AttributeID is a BE16 at wire bytes 16..17 of the common MAD header
	// (IBA §13.4.6: BaseVer, MgmtClass, ClassVer, Method, Status, ClassSpec,
	// 64-bit TID, then AttributeID@16). Read straight from the wire payload.
	ibMADAttrIDOff = 16

	// SMP payload is copied from MAD+64 (see libibmad: IB_SMP_DATA_OFFS).
	ibSMPDataOff = 64

	ibAttrNodeDesc   = 0x0010
	ibAttrNodeInfo   = 0x0011
	ibAttrPortInfo   = 0x0015
	ibAttrSMInfo     = 0x0020
	ibClassSMI       = 0x01
	ibClassSMIDirect = 0x81

	// SMInfo (IBA Vol.1 §14.2.5.13, Table 120) values for the mock master SM.
	// sminfo prints ActCount and Priority verbatim; the mock has no live SM, so
	// they are deterministic constants rather than mutable state.
	smStateMaster     = 3      // SMINFO_MASTER
	defaultSMPriority = 15     // cosmetic
	defaultSMKey      = 0      // unprotected subnet
	smActCount        = 0x1000 // deterministic, non-zero

	// PortInfo / NodeInfo values understood by libibmad / iblinkinfo.
	portStateActive     = 4 // PORT_ACTIVE
	portPhysStateLinkUp = 5
	linkWidth4X         = 2
	linkSpeed10G        = 4
	nodeTypeCA          = 1
	defaultSMLID        = 1
	defaultGidPrefix    = 0xfe80000000000000
)

// TrySynthesize returns a RECV umad buffer for subnet GETs, or (nil, false).
// localCA is the umad-opened HCA (e.g. mlx5_0) used to resolve self-queries (dlid 0).
func TrySynthesize(sendMad []byte, g *fabric.Graph, localCA string) ([]byte, bool) {
	if g == nil || len(sendMad) < umadMADOffset+16 {
		return nil, false
	}
	mad := sendMad[umadMADOffset:]
	if len(mad) < 24 {
		return nil, false
	}

	// MAD wire header (IBA §13.4): BaseVer | MgmtClass | ClassVer | Method,
	// then Status, TID, AttrID (BE16 @16), Reserved, AttrMod (BE32 @20).
	// libibumad delivers MADs in wire byte order, so read every field straight
	// from the payload — same convention as IsSMPSend and the SA decoder. A
	// previous version word-swapped the header first and then read hdr[1],
	// which is ClassVersion (0x01 for almost every class), so any vendor/GSI
	// MAD with ClassVersion 0x01 was misclassified as SMI.
	cls := mad[1]
	if cls != ibClassSMI && cls != ibClassSMIDirect {
		return nil, false
	}
	method := mad[ibMADMethodOff] & 0x7f
	if method != 0x01 { // GET
		return nil, false
	}
	attrID := binary.BigEndian.Uint16(mad[ibMADAttrIDOff : ibMADAttrIDOff+2])

	// SMInfo (sminfo) reports a fabric-global SM identity, not a DR-target
	// attribute. sminfo LID-routes it to the advertised SM LID (defaultSMLID=1),
	// which is not a real port in the graph, so resolveTarget below would reject
	// it. Answer it here, before any target/DLID resolution, from the elected
	// master SM (lowest-PortGUID port) so every pod reports the same identity.
	if attrID == ibAttrSMInfo {
		sm, smOK := g.MasterSM()
		if !smOK {
			return nil, false
		}
		out := newSMPResp(sendMad, method)
		fillSMInfo(out[umadMADOffset:], sm)
		smpLogf("attr=0x%04x SMInfo -> master sm guid=%s lid=0x%x state=MASTER", attrID, sm.PortGUID, sm.LID)
		return out, true
	}

	attrMod := binary.BigEndian.Uint32(mad[20:24])

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

// newSMPResp clones a SEND umad into a GETRESP buffer: padded to at least a full
// legacy umad frame (so short libibmad sends still carry the 256-byte MAD),
// umad.status zeroed (a non-zero status makes libibmad discard the reply), and
// the MAD method's response bit (0x80) set. It mirrors the inline response setup
// in TrySynthesize and is used by the SMInfo branch.
func newSMPResp(sendMad []byte, method byte) []byte {
	out := make([]byte, len(sendMad))
	if len(sendMad) < minUmadLen {
		out = make([]byte, minUmadLen)
	}
	copy(out, sendMad)
	if len(out) >= 8 {
		binary.LittleEndian.PutUint32(out[4:8], 0)
	}
	out[umadMADOffset+ibMADMethodOff] = method | 0x80
	return out
}

// fillSMInfo writes a master-SM SMInfo payload (AttrID 0x0020) for sm into the
// SMP data block. Layout per IBA Vol.1 §14.2.5.13 Table 120 / libibmad fields.c:
// GUID {0,64}, SM_Key {64,64}, ActCount {128,32}, Priority {160,4}, SMState
// {164,4}. The two 64-bit fields are plain big-endian (putGUID64 / SetField64),
// matching how NodeGuid/PortGuid/GidPrefix are written; the sub-32-bit fields use
// the libibmad BITSOFFS convention via SetFieldSpec, so byte 20 of the block ends
// up (Priority<<4)|SMState.
func fillSMInfo(mad []byte, sm fabric.Port) {
	if len(mad) < ibSMPDataOff+64 {
		return
	}
	pl := mad[ibSMPDataOff:]
	for i := range pl {
		pl[i] = 0
	}
	putGUID64(pl, 0, sm.PortGUID)    // GUID @ bit 0
	SetField64(pl, 64, defaultSMKey) // SM_Key @ bit 64
	SetFieldSpec(pl, 128, 32, smActCount)
	SetFieldSpec(pl, 160, 4, defaultSMPriority)
	SetFieldSpec(pl, 164, 4, smStateMaster)
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
	SetFieldSpec(pl, 24, 8, 1)    // NumPorts
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
