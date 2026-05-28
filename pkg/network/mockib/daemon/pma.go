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
	pmaClass     byte = 0x04
	pmaMethodGet byte = 0x01
	pmaMethodSet byte = 0x02
)

// PMAAttrXxx are exposed so test fixtures and the server-side
// dispatcher can reference them without rebinding the literals.
const (
	PMAAttrClassPortInfo   uint16 = 0x0001
	PMAAttrPortCounters    uint16 = 0x0012
	PMAAttrPortCountersExt uint16 = 0x001D
)

const (
	pmaUMADHeaderLen = 56
	pmaMADMethodOff  = 3  // wire byte offset of Method in the MAD header (IBA §13.4)
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
//
// MAD wire layout (IBA §13.4) puts MgmtClass at byte 1 and Method at
// byte 3 of the MAD header. libibumad delivers the MAD payload to the
// daemon in wire (network) byte order, so we read those bytes directly.
//
// A previous version ran the header through subnet.NormalizeMADHeader
// (a 4-byte-word swap that assumes the bytes are already in host-word
// order) and then read hdr[1] / hdr[3]. After that swap hdr[1] is
// ClassVersion (0x01 for nearly every IB management class) and hdr[3]
// is BaseVersion (also 0x01), so PMA MADs (MgmtClass=0x04, Method=Get)
// were misclassified as non-PMA — IsPMASend returned false for every
// real perfquery request, the dispatcher was never called, and
// handleSend fell through to the loopback echo, returning a response
// whose counter fields were all zero (proven on the live demo cluster
// where sysfs port_xmit_data was ~1.27e9 yet perfquery printed 0).
//
// Same trap as the IsSMPSend fix in subnet/drpath.go; same shape of fix.
func IsPMASend(umad []byte) bool {
	if len(umad) < pmaUMADMinLen {
		return false
	}
	mad := umad[56:]
	if mad[1] != pmaClass {
		return false
	}
	if mad[3]&0x80 != 0 {
		return false
	}
	method := mad[3] & 0x7f
	return method == pmaMethodGet || method == pmaMethodSet
}

// TrySynthesizePMA returns a synthesized RECV umad for the given PMA
// request, or (nil,false) when the request is unsupported.
//
// The MAD header is read directly from wire byte order (no
// NormalizeMADHeader), see IsPMASend's comment for why — the swap was
// reading ClassVersion instead of MgmtClass and made every real
// perfquery request fall through to loopback echo.
func TrySynthesizePMA(sendMad []byte, localCA string,
	gen counters.Generator, epochs *counters.Epochs, now time.Time) ([]byte, bool) {
	if len(sendMad) < pmaUMADHeaderLen+pmaMADMinLen {
		return nil, false
	}
	mad := sendMad[pmaUMADHeaderLen:]
	if mad[1] != pmaClass {
		return nil, false
	}
	if mad[pmaMADMethodOff]&0x80 != 0 {
		return nil, false
	}
	method := mad[pmaMADMethodOff] & 0x7f
	if method != pmaMethodGet && method != pmaMethodSet {
		return nil, false
	}
	attrID := decodePMAAttrID(mad)

	caIdx, ok := caIndex(localCA)
	if !ok {
		return nil, false
	}

	if method == pmaMethodSet {
		// ClassPortInfo Set is not defined for this stack; only counter
		// resets are accepted. Any other AttrID falls through to nil/false
		// so the SMP/loopback paths can't accidentally see it.
		if attrID != PMAAttrPortCounters && attrID != PMAAttrPortCountersExt {
			return nil, false
		}
		out := buildPMAResponse(sendMad, method)
		payload := out[pmaUMADHeaderLen+pmaDataOff:]
		reqPayload := mad[pmaDataOff:]
		portSelect := subnet.GetFieldSpec(reqPayload, 0, 8)
		counterSelect := subnet.GetFieldSpec(reqPayload, 16, 16)
		subnet.SetFieldSpec(payload, 0, 8, portSelect)
		subnet.SetFieldSpec(payload, 16, 16, counterSelect)
		// Single per-port epoch covers both legacy and extended attrs —
		// perfquery -R typically targets one and assumes both are cleared
		// (sysfs reflects the writer's view of the same Generator).
		epochs.Reset(caIdx, now)
		return out, true
	}

	// method == pmaMethodGet (other methods rejected above).
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

// fillPMAClassPortInfo writes a minimal-but-functional PerfMgt
// ClassPortInfo. perfquery's dump_perfcounters checks CapabilityMask
// before reading IB_PC_EXT_* fields and prints
//
//	"PerfMgt ClassPortInfo CapMask 0x00; No extended counter support indicated"
//
// when the mask is empty — which then causes every PortXmitData /
// PortRcvData / PortXmitPkts / PortRcvPkts cell in `perfquery -x` to
// render as 0 regardless of what TrySynthesizePMA actually packed in
// the response. The bits we set match real ConnectX-class hardware:
//
//	0x0100 ALL_PORT_SELECT_SUP — perfquery -a / "summary" support
//	0x0200 EXT_WIDTH_SUPPORTED — IB_PC_EXT_XMIT_BYTES_F etc are populated
//	0x0400 EXT_WIDTH_NOIETF_SUP — extended widths are non-IETF wire layout
func fillPMAClassPortInfo(payload []byte) {
	subnet.SetFieldSpec(payload, 0, 8, 1)        // BaseVersion
	subnet.SetFieldSpec(payload, 8, 8, 1)        // ClassVersion
	subnet.SetFieldSpec(payload, 16, 16, 0x0700) // CapabilityMask (ALL_PORT + EXT_WIDTH + EXT_NOIETF)
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

// decodePMAAttrID reads AttributeID from a wire-order MAD payload.
// IBA §13.4: AttrID sits at MAD bytes 16..17, big-endian. libibumad
// delivers wire byte order to the daemon, so a single BE16 read is the
// right thing — the previous version routed through NormalizeMADHeader
// and used a double-swap that happened to land on the right value by
// coincidence, see the IsPMASend comment for context.
func decodePMAAttrID(mad []byte) uint16 {
	return binary.BigEndian.Uint16(mad[16:18])
}
