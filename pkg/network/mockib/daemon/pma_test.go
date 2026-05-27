// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
)

// buildPMAUMAD returns a minimal umad buffer with the requested class +
// method + attrID. The on-wire MAD is "word-swapped" relative to plain
// big-endian — we mirror what libibmad emits by writing the class byte at
// MAD byte 1 (which corresponds to the second byte of the first 4-byte
// word once swapped). For test purposes we directly write the
// already-swapped buffer.
func buildPMAUMAD(class, method byte, attrID uint16) []byte {
	buf := make([]byte, 56+256)
	mad := buf[56:]
	// In the swapped header, word 0 (mad[0:4]) holds BaseVersion(0),
	// MgmtClass(1), ClassVersion(2), Method(3). After the wire-side
	// swap done by normalizeMADHeader, those land at hdr[0..3] as
	// mad[3], mad[2], mad[1], mad[0]. So to put class at hdr[1] we
	// write mad[2]=class; to put method at hdr[3] we write mad[0]=method.
	mad[0] = method
	mad[1] = 1 // ClassVersion
	mad[2] = class
	mad[3] = 1 // BaseVersion
	// AttrID at hdr[18..19] = decodeAttrID(hdr) does a byte-swap, so we
	// need to write the swapped bytes into the swapped layout. word 4
	// (mad[16..19]) holds Status(16..17), Reserved(18), AttrID(20..21)?
	// libibmad layout: bytes 16-17 = Status, 18-19 = ClassSpecific, 20-21 = TID hi, ...
	// and AttrID is at MAD bytes 16-17 in the *unswapped* on-wire layout? No.
	// Per libibmad mad.h: BaseVersion(0) MgmtClass(1) ClassVersion(2) Method(3)
	// Status(4..5) ClassSpecific(6..7) TID(8..15) AttrID(16..17) Reserved(18..19)
	// AttrModifier(20..23). The "word-swapped" view used by
	// normalizeMADHeader reorders bytes within each 4-byte word, so
	// AttrID's bytes 16-17 land at hdr[18..19] after swap (mad[19],mad[18]).
	// We write the swapped pair: mad[18]=lo, mad[19]=hi.
	mad[19] = byte(attrID >> 8)
	mad[18] = byte(attrID & 0xff)
	return buf
}

func TestIsPMASend_AcceptsGet(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCounters)
	if !IsPMASend(umad) {
		t.Fatal("class=0x04 method=Get must be a PMA send")
	}
}

func TestIsPMASend_AcceptsSet(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x02, PMAAttrPortCounters)
	if !IsPMASend(umad) {
		t.Fatal("class=0x04 method=Set must be a PMA send")
	}
}

func TestIsPMASend_RejectsGetResp(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x81, PMAAttrPortCounters)
	if IsPMASend(umad) {
		t.Fatal("class=0x04 method=GetResp must NOT be classified as send")
	}
}

func TestIsPMASend_RejectsSMI(t *testing.T) {
	umad := buildPMAUMAD(0x01, 0x01, 0x0011)
	if IsPMASend(umad) {
		t.Fatal("class=0x01 (SMI) must not be classified as PMA send")
	}
}

func TestIsPMASend_RejectsShortBuffer(t *testing.T) {
	if IsPMASend(nil) {
		t.Fatal("nil buffer must not be a PMA send")
	}
	if IsPMASend(make([]byte, 16)) {
		t.Fatal("short buffer must not be a PMA send")
	}
}

func TestTrySynthesizePMA_SkeletonReturnsFalse(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCounters)
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now())
	out, ok := TrySynthesizePMA(umad, "mlx5_0", gen, epochs, time.Now())
	if ok || out != nil {
		t.Fatalf("skeleton must return (nil,false); got ok=%v len=%d", ok, len(out))
	}
}
