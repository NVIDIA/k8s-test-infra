// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/subnet"
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
	// AttrID in normalized hdr[18..19]; mirror subnet/synthesize_test.go:
	// pre-swap the attr, then word-scramble so mad[17]=hdr[18], mad[16]=hdr[19].
	swapped := (attrID >> 8) | (attrID << 8)
	mad[17] = byte(swapped >> 8)
	mad[16] = byte(swapped)
	return buf
}

func getPMAFieldSpec(t *testing.T, recvUmad []byte, specOff, width int) uint32 {
	t.Helper()
	if len(recvUmad) < 56+64+24 {
		t.Fatalf("recv too short: %d", len(recvUmad))
	}
	payload := recvUmad[56+64:]
	return subnet.GetFieldSpec(payload, specOff, width)
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

func TestTrySynthesizePMA_ClassPortInfo(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrClassPortInfo)
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now())
	out, ok := TrySynthesizePMA(umad, "mlx5_0", gen, epochs, time.Now())
	if !ok || out == nil {
		t.Fatal("ClassPortInfo Get must be synthesized")
	}
	if len(out) < 56+24 {
		t.Fatalf("response too short: %d", len(out))
	}
	if out[56+3] != (0x01 | 0x80) {
		t.Fatalf("method = 0x%02x, want 0x81 (GetResp)", out[56+3])
	}
	if status := binary.LittleEndian.Uint32(out[4:8]); status != 0 {
		t.Fatalf("status = 0x%x, want 0", status)
	}
	if v := getPMAFieldSpec(t, out, 0, 8); v != 1 {
		t.Fatalf("BaseVersion = %d, want 1", v)
	}
	if v := getPMAFieldSpec(t, out, 8, 8); v != 1 {
		t.Fatalf("ClassVersion = %d, want 1", v)
	}
}

func TestTrySynthesizePMA_PortCounters_NonZero(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCounters)
	payload := umad[56+64:]
	subnet.SetFieldSpec(payload, 0, 8, 1)
	subnet.SetFieldSpec(payload, 16, 16, 0xffff)

	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now().Add(-time.Hour))
	out, ok := TrySynthesizePMA(umad, "mlx5_0", gen, epochs, time.Now())
	if !ok || out == nil {
		t.Fatal("PortCounters Get must be synthesized")
	}
	if out[56+3] != 0x81 {
		t.Fatalf("method = 0x%02x, want 0x81", out[56+3])
	}
	if v := getPMAFieldSpec(t, out, 0, 8); v != 1 {
		t.Fatalf("PortSelect echo = %d, want 1", v)
	}
	if v := getPMAFieldSpec(t, out, 16, 16); v != 0xffff {
		t.Fatalf("CounterSelect echo = 0x%x, want 0xffff", v)
	}
	if v := getPMAFieldSpec(t, out, 192, 32); v == 0 {
		t.Fatal("port_xmit_data must be non-zero")
	}
	if v := getPMAFieldSpec(t, out, 224, 32); v == 0 {
		t.Fatal("port_rcv_data must be non-zero")
	}
}

func TestTrySynthesizePMA_PortCounters_AgreesWithGenerator(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCounters)
	payload := umad[56+64:]
	subnet.SetFieldSpec(payload, 0, 8, 1)
	subnet.SetFieldSpec(payload, 16, 16, 0xffff)
	epochs := counters.NewEpochs(time.Now().Add(-30 * time.Second))
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	now := time.Now()
	out, ok := TrySynthesizePMA(umad, "mlx5_2", gen, epochs, now)
	if !ok {
		t.Fatal("synthesis failed")
	}
	e := counters.FindByName("port_xmit_data")
	if e == nil {
		t.Fatal("catalog missing port_xmit_data")
	}
	want := uint32(gen.Value(2, *e, epochs.Elapsed(2, now)))
	got := getPMAFieldSpec(t, out, 192, 32)
	if got != want {
		t.Fatalf("port_xmit_data: pma=%d gen=%d", got, want)
	}
}

func TestTrySynthesizePMA_RejectsUnknownCA(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCounters)
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now())
	if _, ok := TrySynthesizePMA(umad, "wat0", gen, epochs, time.Now()); ok {
		t.Fatal("unknown CA name must yield (nil,false)")
	}
}

func TestTrySynthesizePMA_RejectsUnsupportedAttr(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, 0x0FFF)
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now())
	if _, ok := TrySynthesizePMA(umad, "mlx5_0", gen, epochs, time.Now()); ok {
		t.Fatal("unsupported AttrID must yield (nil,false)")
	}
}
