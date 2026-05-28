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
// method + attrID in real wire byte order, which is what libibmad
// actually emits and what libibumad delivers to the daemon.
//
// MAD wire layout (IBA §13.4) for the first 4 bytes is
//
//	BaseVersion | MgmtClass | ClassVersion | Method
//
// and AttrID is a big-endian uint16 at bytes 16..17. A previous version
// of this helper instead pre-scrambled the bytes as if the daemon would
// run them through subnet.NormalizeMADHeader and read hdr[1] — that
// matched the (buggy) IsPMASend / TrySynthesizePMA implementations but
// produced MADs that no real perfquery would ever send. Fixing
// IsPMASend to read mad[1] directly exposed the test fixture too.
func buildPMAUMAD(class, method byte, attrID uint16) []byte {
	buf := make([]byte, 56+256)
	mad := buf[56:]
	mad[0] = 1 // BaseVersion
	mad[1] = class
	mad[2] = 1 // ClassVersion
	mad[3] = method
	binary.BigEndian.PutUint16(mad[16:18], attrID)
	return buf
}

// perfqueryPortCountersHdr is the first 24 bytes of a real `perfquery
// <lid> 1` MAD send captured via the libibumad shim, in wire byte order:
//
//	mad_hdr = 01 04 01 01  00000000 ...
//	          BaseVer Class ClassVer Method
//	AttrID@16..17 = 00 12 (PortCounters)
//
// The previous IsPMASend implementation ran this through
// subnet.NormalizeMADHeader and then checked hdr[1] (which after the
// word swap is ClassVersion = 0x01, not MgmtClass = 0x04), so it
// returned false for every real perfquery request and the dispatcher
// was never called. The synthesized response that loopback echoed back
// looked superficially OK to perfquery but every counter cell printed
// 0 — proven on the live demo cluster against a non-zero sysfs
// port_xmit_data. This fixture pins the wire format so the regression
// can't quietly come back.
var perfqueryPortCountersHdr = []byte{
	0x01, 0x04, 0x01, 0x01, // BaseVer, MgmtClass=PMA, ClassVer, Method=Get
	0x00, 0x00, 0x00, 0x00, // Status
	0x00, 0x00, 0x00, 0x00, 0x12, 0x34, 0x56, 0x78, // TransactionID
	0x00, 0x12, 0x00, 0x00, // AttributeID=PortCounters, Reserved
	0x00, 0x00, 0x00, 0x00, // AttributeModifier
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

// TestIsPMASend_AcceptsRealWireFormat replays a real perfquery MAD
// captured off the libibumad shim. This is the regression test for the
// byte-order bug that made `validate-perfquery.sh` fail with all-zero
// counter cells even though the sysfs writer was producing ~1.27e9
// PortXmitData on the live demo cluster. Before the fix IsPMASend
// read ClassVersion(0x01) instead of MgmtClass(0x04) — i.e. it
// returned false for every real perfquery request and the dispatcher
// was never invoked.
func TestIsPMASend_AcceptsRealWireFormat(t *testing.T) {
	umad := make([]byte, 56+256)
	copy(umad[56:], perfqueryPortCountersHdr)
	if !IsPMASend(umad) {
		t.Fatalf("real wire-format PMA Get (mad[1]=0x%02x mad[3]=0x%02x) must be PMA send",
			umad[57], umad[59])
	}
}

// TestIsPMASend_DecodesRealAttrID locks down decodePMAAttrID against
// the captured fixture so a future refactor of TrySynthesizePMA can't
// silently start reading AttrID from the wrong offset (the previous
// double-swap implementation worked by coincidence, see decodePMAAttrID).
func TestIsPMASend_DecodesRealAttrID(t *testing.T) {
	if got := decodePMAAttrID(perfqueryPortCountersHdr); got != PMAAttrPortCounters {
		t.Fatalf("decodePMAAttrID on real wire MAD: got %#x want %#x", got, PMAAttrPortCounters)
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

// getPMAField64 reads a 64-bit big-endian field at a byte-aligned PMA
// payload offset (relative to MAD+64).
func getPMAField64(t *testing.T, recvUmad []byte, bitOff int) uint64 {
	t.Helper()
	payload := recvUmad[56+64:]
	off := bitOff / 8
	if off+8 > len(payload) {
		t.Fatalf("payload too short: %d for off %d", len(payload), bitOff)
	}
	return binary.BigEndian.Uint64(payload[off : off+8])
}

func TestTrySynthesizePMA_PortCountersExt_NonZero(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCountersExt)
	payload := umad[56+64:]
	subnet.SetFieldSpec(payload, 0, 8, 1)
	subnet.SetFieldSpec(payload, 16, 16, 0xffff)

	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now().Add(-time.Hour))
	out, ok := TrySynthesizePMA(umad, "mlx5_0", gen, epochs, time.Now())
	if !ok || out == nil {
		t.Fatal("PortCountersExt Get must be synthesized")
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
	if v := getPMAField64(t, out, 64); v == 0 {
		t.Fatal("port_xmit_data_64 must be non-zero")
	}
	// 64-bit response must exceed uint32 range at 400Gbps * 1h.
	if v := getPMAField64(t, out, 64); v <= 0xffffffff {
		t.Fatalf("expected 64-bit xmit_data > uint32 max, got %d", v)
	}
}

func TestTrySynthesizePMA_PortCountersExt_AgreesWithGenerator(t *testing.T) {
	umad := buildPMAUMAD(0x04, 0x01, PMAAttrPortCountersExt)
	payload := umad[56+64:]
	subnet.SetFieldSpec(payload, 0, 8, 1)
	subnet.SetFieldSpec(payload, 16, 16, 0xffff)
	epochs := counters.NewEpochs(time.Now().Add(-30 * time.Second))
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	now := time.Now()
	out, ok := TrySynthesizePMA(umad, "mlx5_3", gen, epochs, now)
	if !ok {
		t.Fatal("synthesis failed")
	}
	for _, c := range []struct {
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
	} {
		e := counters.FindByName(c.name)
		if e == nil {
			t.Fatalf("catalog missing %s", c.name)
		}
		want := gen.Value(3, *e, epochs.Elapsed(3, now))
		got := getPMAField64(t, out, c.bitOff)
		if got != want {
			t.Errorf("%s: pma=%d gen=%d", c.name, got, want)
		}
	}
}

func TestTrySynthesizePMA_PortCountersSet_ResetsEpoch(t *testing.T) {
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now().Add(-time.Hour))

	// 1. Pre-reset Get returns large value.
	getReq := buildPMAUMAD(0x04, 0x01, PMAAttrPortCountersExt)
	pl := getReq[56+64:]
	subnet.SetFieldSpec(pl, 0, 8, 1)
	subnet.SetFieldSpec(pl, 16, 16, 0xffff)
	before, ok := TrySynthesizePMA(getReq, "mlx5_0", gen, epochs, time.Now())
	if !ok {
		t.Fatal("pre-reset Get must succeed")
	}
	preXmit := getPMAField64(t, before, 64)
	if preXmit == 0 {
		t.Fatal("pre-reset xmit_data must be non-zero")
	}

	// 2. Set ClearCounters via PortCounters (legacy 32-bit attr).
	setReq := buildPMAUMAD(0x04, 0x02, PMAAttrPortCounters)
	setPl := setReq[56+64:]
	subnet.SetFieldSpec(setPl, 0, 8, 1)
	subnet.SetFieldSpec(setPl, 16, 16, 0xffff) // CounterSelect = all
	setResp, ok := TrySynthesizePMA(setReq, "mlx5_0", gen, epochs, time.Now())
	if !ok || setResp == nil {
		t.Fatal("Set ClearCounters must be acknowledged")
	}
	if setResp[56+3] != (0x02 | 0x80) {
		t.Fatalf("Set response method = 0x%02x, want 0x82 (Set|GetResp bit)", setResp[56+3])
	}
	// Echo PortSelect / CounterSelect.
	if v := getPMAFieldSpec(t, setResp, 0, 8); v != 1 {
		t.Fatalf("Set response PortSelect echo = %d, want 1", v)
	}
	if v := getPMAFieldSpec(t, setResp, 16, 16); v != 0xffff {
		t.Fatalf("Set response CounterSelect echo = 0x%x, want 0xffff", v)
	}

	// 3. Post-reset Get returns value <= before (because elapsed since
	// reset is now sub-millisecond instead of 1h).
	after, ok := TrySynthesizePMA(getReq, "mlx5_0", gen, epochs, time.Now())
	if !ok {
		t.Fatal("post-reset Get must succeed")
	}
	postXmit := getPMAField64(t, after, 64)
	if postXmit >= preXmit {
		t.Fatalf("expected post-reset xmit_data < pre-reset: pre=%d post=%d", preXmit, postXmit)
	}
}

func TestTrySynthesizePMA_PortCountersExtSet_AlsoResets(t *testing.T) {
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now().Add(-time.Hour))

	setReq := buildPMAUMAD(0x04, 0x02, PMAAttrPortCountersExt)
	setPl := setReq[56+64:]
	subnet.SetFieldSpec(setPl, 0, 8, 1)
	subnet.SetFieldSpec(setPl, 16, 16, 0xffff)
	resp, ok := TrySynthesizePMA(setReq, "mlx5_1", gen, epochs, time.Now())
	if !ok || resp == nil {
		t.Fatal("Set Ext must be acknowledged")
	}
	if resp[56+3] != 0x82 {
		t.Fatalf("Set Ext response method = 0x%02x, want 0x82", resp[56+3])
	}

	// Verify epoch on caIdx=1 was reset, but caIdx=0 untouched.
	getReq := buildPMAUMAD(0x04, 0x01, PMAAttrPortCountersExt)
	gp := getReq[56+64:]
	subnet.SetFieldSpec(gp, 0, 8, 1)
	subnet.SetFieldSpec(gp, 16, 16, 0xffff)
	now := time.Now()
	g0, _ := TrySynthesizePMA(getReq, "mlx5_0", gen, epochs, now)
	g1, _ := TrySynthesizePMA(getReq, "mlx5_1", gen, epochs, now)
	v0 := getPMAField64(t, g0, 64)
	v1 := getPMAField64(t, g1, 64)
	if v0 == 0 || v1 >= v0 {
		t.Fatalf("expected caIdx 0 large, caIdx 1 small: v0=%d v1=%d", v0, v1)
	}
}

func TestTrySynthesizePMA_ClassPortInfoSet_Rejected(t *testing.T) {
	setReq := buildPMAUMAD(0x04, 0x02, PMAAttrClassPortInfo)
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now())
	if _, ok := TrySynthesizePMA(setReq, "mlx5_0", gen, epochs, time.Now()); ok {
		t.Fatal("Set ClassPortInfo must be rejected")
	}
}
