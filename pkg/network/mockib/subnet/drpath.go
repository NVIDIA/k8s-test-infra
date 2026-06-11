// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
)

const (
	// MAD header fields for SMP Direct Route (libibmad fields.c BE_OFFS / bit 1024).
	ibDRHopCntBit   = 32
	ibDRPathByteOff = 1024 / 8 // DrSmpPath array starts at bit 1024
)

// IsSMPSend reports whether umad carries a subnet management GET/SET.
//
// MAD wire layout (IBA §13.4) puts MgmtClass at byte 1 of the MAD header.
// libibumad delivers the MAD payload to the daemon in wire (network) byte
// order, so we read MgmtClass directly. A previous version of this function
// ran the header through normalizeMADHeader (which 4-byte-word-swaps as if
// the bytes were in host order) and then read hdr[1] — that ended up reading
// ClassVersion, which is 0x01 for almost every IB management class.
//
// The consequence was that vendor OpenIB ping MADs (MgmtClass=0x32,
// ClassVersion=0x01) were misclassified as SMI (0x01) and short-circuited
// the cross-pod fabric path in handleSend:
//
//	if s.cfg.Fabric && !subnet.IsSMPSend(req.MAD) && s.tryFabricSend(...) {
//
// With IsSMPSend wrongly returning true, ibping MADs were never routed to
// the peer over TCP and never produced a synthetic recv either, so ibping's
// umad_recv timed out with EAGAIN ("recv failed: Resource temporarily
// unavailable") on every attempt — including the ibping-multinode CI job
// and `docs/demo/standalone/demo.sh`. iblinkinfo (real SMI, ClassVersion
// also 0x01) only worked by the same coincidence.
//
// Reading MgmtClass directly fixes vendor MADs without changing SMI/SMIDir
// behaviour (their MgmtClass values 0x01 / 0x81 land on the right byte too).
func IsSMPSend(umad []byte) bool {
	if len(umad) < umadMADOffset+24 {
		return false
	}
	cls := umad[umadMADOffset+1]
	return cls == ibClassSMI || cls == ibClassSMIDirect
}

func resolveTarget(g *fabric.Graph, mad []byte, lid uint16, localCA string) (fabric.Port, bool) {
	if g == nil {
		return fabric.Port{}, false
	}
	if p, ok := g.ByLID(lid); ok {
		return p, true
	}

	hopCnt := drHopCnt(mad)
	if hopCnt < 0 || hopCnt > 64 {
		hopCnt = 0
	}
	// ib_resolve_self uses DLID 0/0xffff with hopCnt=0; everything with hopCnt>0
	// is a genuine outbound DR walk (path[0] is reserved per IB spec 14.2.1.2).
	if (lid == 0 || lid == 0xffff) && hopCnt <= 0 {
		return g.ByCAName(localCA)
	}

	current, ok := g.ByCAName(localCA)
	if !ok {
		return fabric.Port{}, false
	}
	for i := 0; i < hopCnt; i++ {
		// path[0] is reserved; per-hop outbound port is path[i+1].
		outPort := drPathByte(mad, i+1)
		current, ok = g.PeerAtOutbound(current, outPort, i)
		if !ok {
			return fabric.Port{}, false
		}
	}
	return current, true
}

func drPathByte(mad []byte, idx int) uint8 {
	off := ibDRPathByteOff + idx
	if off < 0 || off >= len(mad) {
		return 0
	}
	return mad[off]
}

func drHopCnt(mad []byte) int {
	// HopCnt lives at MAD byte 7 (spec bit offset 56). libibmad's BITSOFFS maps
	// that to bitOff 32 in our word-swapped buffer view.
	return int(GetField(mad, ibDRHopCntBit, 8))
}
