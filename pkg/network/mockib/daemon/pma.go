// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/subnet"
)

// PMA wire constants (IB spec vol 1, ch 16; libibmad mad.h). MgmtClass
// 0x04 is Performance Management; Get/Set carry counter queries from
// perfquery and Prometheus RDMA exporters.
const (
	pmaClass         byte = 0x04
	pmaMethodGet     byte = 0x01
	pmaMethodSet     byte = 0x02
	pmaMethodGetResp byte = 0x81
)

// PMAAttrXxx are exposed so test fixtures and the dispatcher hook (Task
// 3.5) can reference them without rebinding the literals.
const (
	PMAAttrClassPortInfo   uint16 = 0x0001
	PMAAttrPortCounters    uint16 = 0x0012
	PMAAttrPortCountersExt uint16 = 0x001D
)

const (
	pmaUMADHeaderLen = 56
	pmaMADMethodOff  = 3 // unswapped byte offset of Method in MAD header
	pmaDataOff       = 64 // IB_PC_DATA_OFFS (matches IB_SMP_DATA_OFFS)
	pmaMADMinLen     = 24 // MAD header bytes
	pmaMADTotalLen   = 256 // perfquery sends full 256-byte MAD payload
	pmaUMADFullLen   = pmaUMADHeaderLen + pmaMADTotalLen
)

// pmaPortCountersFields maps catalog counter names to their PMA
// PortCounters bit-field offsets (libibmad fields.c IB_PC_*). Order is
// stable for readability; the dispatcher iterates it once.
var pmaPortCountersFields = []struct {
	name    string
	specOff int
	width   int
}{
	{"symbol_error", 32, 16},
	{"link_error_recovery", 48, 8},
	{"link_downed", 56, 8},
	{"port_rcv_errors", 64, 16},
	{"port_rcv_remote_physical_errors", 80, 16},
	{"port_rcv_switch_relay_errors", 96, 16},
	{"port_xmit_discards", 112, 16},
	{"port_xmit_constraint_errors", 128, 8},
	{"port_rcv_constraint_errors", 136, 8},
	{"local_link_integrity_errors", 152, 4},
	{"excessive_buffer_overrun_errors", 156, 4},
	{"VL15_dropped", 176, 16},
	{"port_xmit_data", 192, 32},
	{"port_rcv_data", 224, 32},
	{"port_xmit_packets", 256, 32},
	{"port_rcv_packets", 288, 32},
}

// pmaPortCountersExtFields maps catalog counter names to their
// PortCountersExtended bit-field offsets (libibmad fields.c
// IB_PC_EXT_*). All fields are 64 bits, byte-aligned, so the writer
// uses subnet.SetField64.
var pmaPortCountersExtFields = []struct {
	name   string
	bitOff int
}{
	{"port_xmit_data_64", 64},
	{"port_rcv_data_64", 128},
	{"port_xmit_packets_64", 192},
	{"port_rcv_packets_64", 256},
	{"unicast_xmit_packets", 320},
	{"unicast_rcv_packets", 384},
	{"multicast_xmit_packets", 448},
	{"multicast_rcv_packets", 512},
}

// pmaUMADMinLen is the smallest umad+mad payload we'll touch. Matches
// the SMP path constant from subnet.synthesize.go.
const pmaUMADMinLen = 56 + 24 // umad header + MAD header

// IsPMASend reports whether umad carries a Performance Management
// Get or Set. The handler hook in server.go uses this to route PMA MADs
// to TrySynthesizePMA before the SMP / fabric / loopback fallbacks.
func IsPMASend(umad []byte) bool {
	if len(umad) < pmaUMADMinLen {
		return false
	}
	mad := umad[56:]
	hdr, ok := normalizeMADHeaderForPMA(mad)
	if !ok {
		return false
	}
	if hdr[1] != pmaClass {
		return false
	}
	if hdr[3]&0x80 != 0 {
		return false
	}
	method := hdr[3] & 0x7f
	return method == pmaMethodGet || method == pmaMethodSet
}

// TrySynthesizePMA returns a synthesized RECV umad for the given PMA
// request, or (nil,false) when the request is unsupported.
func TrySynthesizePMA(sendMad []byte, localCA string,
	gen counters.Generator, epochs *counters.Epochs, now time.Time) ([]byte, bool) {
	if len(sendMad) < pmaUMADHeaderLen+pmaMADMinLen {
		return nil, false
	}
	mad := sendMad[pmaUMADHeaderLen:]
	hdr, ok := normalizeMADHeaderForPMA(mad)
	if !ok {
		return nil, false
	}
	if hdr[1] != pmaClass {
		return nil, false
	}
	if hdr[pmaMADMethodOff]&0x80 != 0 {
		return nil, false
	}
	method := hdr[pmaMADMethodOff] & 0x7f
	if method != pmaMethodGet {
		// Set/Reset path is Task 3.4.
		return nil, false
	}
	attrID := decodePMAAttrID(hdr)

	caIdx, ok := caIndex(localCA)
	if !ok {
		return nil, false
	}

	out := buildPMAResponse(sendMad, method)
	payload := out[pmaUMADHeaderLen+pmaDataOff:]

	switch attrID {
	case PMAAttrClassPortInfo:
		fillPMAClassPortInfo(payload)
		return out, true
	case PMAAttrPortCounters:
		reqPayload := mad[pmaDataOff:]
		portSelect := subnet.GetFieldSpec(reqPayload, 0, 8)
		counterSelect := subnet.GetFieldSpec(reqPayload, 16, 16)
		fillPMAPortCounters(payload, caIdx, gen, epochs, now, portSelect, counterSelect)
		return out, true
	case PMAAttrPortCountersExt:
		reqPayload := mad[pmaDataOff:]
		portSelect := subnet.GetFieldSpec(reqPayload, 0, 8)
		counterSelect := subnet.GetFieldSpec(reqPayload, 16, 16)
		fillPMAPortCountersExt(payload, caIdx, gen, epochs, now, portSelect, counterSelect)
		return out, true
	default:
		return nil, false
	}
}

// buildPMAResponse copies sendMad into an output buffer sized for a
// full perfquery response, zeros the umad status word, sets the method
// byte to GetResp, and zeros the PMA payload region so the caller only
// needs to fill the fields it computes.
func buildPMAResponse(sendMad []byte, method byte) []byte {
	out := make([]byte, pmaUMADFullLen)
	copy(out, sendMad)
	// umad status word (libibumad layout: little-endian uint32 at byte 4)
	binary.LittleEndian.PutUint32(out[4:8], 0)
	respMAD := out[pmaUMADHeaderLen:]
	respMAD[pmaMADMethodOff] = method | 0x80
	// Zero the data payload so unfilled fields stay 0.
	payload := respMAD[pmaDataOff:]
	for i := range payload {
		payload[i] = 0
	}
	return out
}

func fillPMAClassPortInfo(payload []byte) {
	subnet.SetFieldSpec(payload, 0, 8, 1)   // BaseVersion
	subnet.SetFieldSpec(payload, 8, 8, 1)   // ClassVersion
	subnet.SetFieldSpec(payload, 16, 16, 0) // CapabilityMask
}

func fillPMAPortCounters(payload []byte, caIdx int,
	gen counters.Generator, epochs *counters.Epochs, now time.Time,
	portSelect, counterSelect uint32) {
	subnet.SetFieldSpec(payload, 0, 8, portSelect)
	subnet.SetFieldSpec(payload, 16, 16, counterSelect)
	elapsed := epochs.Elapsed(caIdx, now)
	for _, f := range pmaPortCountersFields {
		e := counters.FindByName(f.name)
		if e == nil {
			continue
		}
		v := uint32(gen.Value(caIdx, *e, elapsed))
		subnet.SetFieldSpec(payload, f.specOff, f.width, v)
	}
}

func fillPMAPortCountersExt(payload []byte, caIdx int,
	gen counters.Generator, epochs *counters.Epochs, now time.Time,
	portSelect, counterSelect uint32) {
	subnet.SetFieldSpec(payload, 0, 8, portSelect)
	subnet.SetFieldSpec(payload, 16, 16, counterSelect)
	elapsed := epochs.Elapsed(caIdx, now)
	for _, f := range pmaPortCountersExtFields {
		e := counters.FindByName(f.name)
		if e == nil {
			continue
		}
		subnet.SetField64(payload, f.bitOff, gen.Value(caIdx, *e, elapsed))
	}
}

func decodePMAAttrID(hdr [24]byte) uint16 {
	// AttrID is at MAD bytes 16..17 in unswapped layout; after the word
	// swap in normalizeMADHeaderForPMA it lands at hdr[18..19] swapped.
	attrID := binary.BigEndian.Uint16(hdr[18:20])
	return (attrID >> 8) | (attrID << 8)
}

// normalizeMADHeaderForPMA mirrors subnet.normalizeMADHeader since that
// helper is package-private. Keep the two implementations identical;
// PMA and SMP share the same word-swapped wire layout.
func normalizeMADHeaderForPMA(mad []byte) ([24]byte, bool) {
	if len(mad) < 24 {
		return [24]byte{}, false
	}
	var hdr [24]byte
	for w := 0; w < len(hdr); w += 4 {
		hdr[w+0] = mad[w+3]
		hdr[w+1] = mad[w+2]
		hdr[w+2] = mad[w+1]
		hdr[w+3] = mad[w+0]
	}
	return hdr, true
}
