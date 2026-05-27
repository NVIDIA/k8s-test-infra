// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
)

const (
	// MAD header fields for SMP Direct Route (libibmad fields.c BE_OFFS / bit 1024).
	ibDRHopCntBit    = 32
	ibDRPathByteOff  = 1024 / 8 // DrSmpPath array starts at bit 1024
)

// IsSMPSend reports whether umad carries a subnet management GET/SET.
func IsSMPSend(umad []byte) bool {
	if len(umad) < umadMADOffset+24 {
		return false
	}
	hdr, ok := normalizeMADHeader(umad[umadMADOffset:])
	if !ok {
		return false
	}
	cls := hdr[1]
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
