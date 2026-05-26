// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// Offsets for SA PathRecord fields within the MAD payload (libibmad fields.c).
const (
	ibSAClass          = 0x03
	ibSAMethodGet      = 0x01
	ibSAAttrPathRecord = 0x35
	ibSADataOff        = 56
	ibPathRecDGIDOff   = ibSADataOff + 8  // bit 64, 128-bit DGID
	ibPathRecDLIDOff   = ibSADataOff + 40 // bit 320, 16-bit DLID
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
	for i := 0; i+2 <= len(mad); i++ {
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

func setSAMethodResponse(mad []byte) {
	if len(mad) > 0 && mad[0]&0x7f == ibSAMethodGet {
		mad[0] |= 0x80
		return
	}
	if attrOff, ok := pathRecordAttrOffset(mad); ok {
		for _, i := range []int{attrOff - 4, attrOff - 2} {
			if i >= 0 && i < len(mad) && (i < 8 || i >= 16) && mad[i]&0x7f == ibSAMethodGet {
				mad[i] |= 0x80
				return
			}
		}
		for i := attrOff; i >= 0; i-- {
			// IB_MAD_TRID_F occupies bytes 8-15; do not treat 0x01 there as GET method.
			if i >= 8 && i < 16 {
				continue
			}
			if mad[i]&0x7f == ibSAMethodGet {
				mad[i] |= 0x80
				return
			}
		}
	}
}

func (s *Server) resolveLIDForGID(dgid []byte) (uint16, bool) {
	if lid, ok := s.loopback.lidForGID(dgid); ok {
		return lid, true
	}
	guid := portGUIDFromGID(dgid)
	if guid == "" {
		return 0, false
	}
	if peer, ok := s.registry.Lookup(guid); ok && peer.LID != 0 {
		return peer.LID, true
	}
	return 0, false
}

func portGUIDFromGID(gid []byte) string {
	if len(gid) != 16 {
		return ""
	}
	b := gid[8:16]
	return registry.NormalizePortGUID(fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7]))
}
