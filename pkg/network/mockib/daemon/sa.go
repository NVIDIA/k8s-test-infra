// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
)

// Offsets for SA PathRecord fields within the MAD payload (libibmad fields.c).
const (
	ibSAMethodGet      = 0x01
	ibSAAttrPathRecord = 0x35
	ibSADataOff        = 56
	ibPathRecDGIDOff   = ibSADataOff + 8  // bit 64, 128-bit DGID
	ibPathRecDLIDOff   = ibSADataOff + 40 // bit 320, 16-bit DLID

	// ibMADCommonHdrLen is the fixed common MAD header length (BaseVer,
	// MgmtClass, ClassVer, Method, Status, ClassSpec, 64-bit TID). AttributeID
	// and everything beyond it begin here.
	ibMADCommonHdrLen = 16
)

// trySAPathQuery handles ib_path_query_via GET PathRecord MADs used by ibping -G.
func (s *Server) trySAPathQuery(h *portHandle, sendMad []byte) bool {
	if !isSAPathRecordGet(sendMad) {
		return false
	}
	dgid, ok := pathRecordDGID(sendMad)
	if !ok {
		return false
	}
	lid, ok := s.resolveLIDForGID(dgid)
	if !ok {
		return false
	}
	resp := synthesizeSAPathRecordResp(sendMad, lid)
	h.mu.Lock()
	h.recvQ = append(h.recvQ, resp)
	h.mu.Unlock()
	return true
}

func isSAPathRecordGet(umad []byte) bool {
	mad := umad[umadMADOffset:]
	if _, ok := pathRecordAttrOffset(mad); !ok {
		return false
	}
	dgid, ok := pathRecordDGID(umad)
	if !ok {
		return false
	}
	// PathRecord GET carries destination (and source) GID; not a zeroed template.
	for _, b := range dgid {
		if b != 0 {
			return true
		}
	}
	return false
}

func pathRecordAttrOffset(mad []byte) (int, bool) {
	// AttributeID lives at or after the 16-byte common MAD header, so begin the
	// scan there: this keeps a 0x0035 inside the 64-bit TID (bytes 8-15) from
	// false-matching as the PathRecord attribute.
	for i := ibMADCommonHdrLen; i+2 <= len(mad); i++ {
		if binary.BigEndian.Uint16(mad[i:i+2]) == ibSAAttrPathRecord {
			return i, true
		}
	}
	return 0, false
}

func pathRecordDGID(umad []byte) ([]byte, bool) {
	mad := umad[umadMADOffset:]
	off, ok := pathRecordDGIDOffset(mad)
	if !ok {
		return nil, false
	}
	out := make([]byte, 16)
	copy(out, mad[off:off+16])
	return out, true
}

// pathRecordDGIDOffset locates PathRecDGid (fe80:: + port GUID) in SA GET requests.
// With libibumad RMPP the field is not always at the fixed IB_SA_DATA_OFFS+8 byte.
func pathRecordDGIDOffset(mad []byte) (int, bool) {
	if attrOff, ok := pathRecordAttrOffset(mad); ok {
		for off := attrOff; off+16 <= len(mad); off++ {
			if mad[off] == 0xfe && mad[off+1] == 0x80 {
				return off, true
			}
		}
	}
	if len(mad) >= ibPathRecDGIDOff+16 {
		return ibPathRecDGIDOff, true
	}
	return 0, false
}

func pathRecordDLIDOffset(mad []byte) (int, bool) {
	dgidOff, ok := pathRecordDGIDOffset(mad)
	if !ok {
		return 0, false
	}
	dlidOff := dgidOff + (ibPathRecDLIDOff - ibPathRecDGIDOff)
	if dlidOff+2 > len(mad) {
		return 0, false
	}
	return dlidOff, true
}

func synthesizeSAPathRecordResp(sendMad []byte, dlid uint16) []byte {
	out := make([]byte, len(sendMad))
	copy(out, sendMad)
	mad := out[umadMADOffset:]
	setSAMethodResponse(mad)
	if len(mad) > 6 {
		// IB_MAD_STATUS_F (bytes 4-5) must be zero for mad_rpc_rmpp to accept the reply.
		mad[4] = 0
		mad[5] = 0
	}
	// libibmad _do_madrpc matches IB_MAD_TRID_F at umad_get_mad()+8 (bytes 8-15 of MAD view).
	if len(mad) >= 16 {
		copy(mad[8:16], sendMad[umadMADOffset+8:umadMADOffset+16])
	}
	if dlidOff, ok := pathRecordDLIDOffset(mad); ok {
		binary.BigEndian.PutUint16(mad[dlidOff:], dlid)
	}
	return out
}

func saMethodResponseSet(mad []byte) bool {
	if len(mad) > 0 && mad[0]&0x80 != 0 {
		return true
	}
	if attrOff, ok := pathRecordAttrOffset(mad); ok {
		for i := attrOff; i >= 0; i-- {
			if i >= 8 && i < 16 {
				continue
			}
			if mad[i]&0x80 != 0 {
				return true
			}
		}
	}
	return false
}

// setSAMethodResponse converts an SA GET into a GETRESP by OR-ing the response
// bit (0x80) onto the method byte. The method's wire offset varies with the SA
// GET layout this daemon observes (attr-4 in the synthetic fixture, byte 3 in a
// standard-aligned MAD), so locate it relative to the AttributeID rather than
// assuming a fixed offset. Two header regions are never written: the 64-bit TID
// (bytes 8-15, used by libibmad to match the reply) and the leading common
// header (BaseVersion/MgmtClass/ClassVersion at bytes 0-2). A previous revision
// special-cased mad[0]==0x01, which unconditionally matched BaseVersion and
// corrupted it to 0x81 without ever flipping the real method byte.
func setSAMethodResponse(mad []byte) {
	attrOff, ok := pathRecordAttrOffset(mad)
	if !ok {
		return
	}
	for _, i := range []int{attrOff - 4, attrOff - 2} {
		if i >= ibMADMethodOff && i < len(mad) && (i < 8 || i >= ibMADCommonHdrLen) && mad[i]&0x7f == ibSAMethodGet {
			mad[i] |= 0x80
			return
		}
	}
	// Reverse-scan toward the method byte. Stop at ibMADMethodOff so the
	// BaseVersion/MgmtClass/ClassVersion bytes are never mistaken for a method.
	for i := attrOff; i >= ibMADMethodOff; i-- {
		if i >= 8 && i < ibMADCommonHdrLen {
			continue
		}
		if mad[i]&0x7f == ibSAMethodGet {
			mad[i] |= 0x80
			return
		}
	}
}

func (s *Server) resolveLIDForGID(dgid []byte) (uint16, bool) {
	if lid, ok := s.loopback.lidForGID(dgid); ok {
		return lid, true
	}
	portGUID := gid.PortGUIDFromBytes(dgid)
	if portGUID == "" {
		return 0, false
	}
	if peer, ok := s.registry.Lookup(portGUID); ok && peer.LID != 0 {
		return peer.LID, true
	}
	return 0, false
}
