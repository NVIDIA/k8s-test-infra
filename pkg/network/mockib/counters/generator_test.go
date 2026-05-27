// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package counters

import (
	"testing"
	"time"
)

func g() Generator { return Generator{NodeID: 0xfa, RateGbps: 400} }

func find(t *testing.T, name string) Entry {
	t.Helper()
	e := FindByName(name)
	if e == nil {
		t.Fatalf("catalog missing %q", name)
	}
	return *e
}

func TestGenerator_DeterministicSameInputs(t *testing.T) {
	gen := g()
	v1 := gen.Value(0, find(t, "port_xmit_data"), 10*time.Second)
	v2 := gen.Value(0, find(t, "port_xmit_data"), 10*time.Second)
	if v1 != v2 {
		t.Fatalf("non-deterministic: %d vs %d", v1, v2)
	}
}

func TestGenerator_DistinctAcrossCAIdx(t *testing.T) {
	gen := g()
	e := find(t, "port_xmit_data")
	if gen.Value(0, e, 0) == gen.Value(1, e, 0) {
		t.Fatalf("seed must differ across caIdx")
	}
}

func TestGenerator_DistinctAcrossNodes(t *testing.T) {
	e := find(t, "port_xmit_data")
	a := Generator{NodeID: 0xfa, RateGbps: 400}.Value(0, e, 0)
	b := Generator{NodeID: 0x0a, RateGbps: 400}.Value(0, e, 0)
	if a == b {
		t.Fatalf("seed must differ across NodeID")
	}
}

func TestGenerator_DataFamilyGrowsOverTime(t *testing.T) {
	gen := g()
	e := find(t, "port_xmit_data_64")
	early := gen.Value(0, e, 1*time.Second)
	late := gen.Value(0, e, 60*time.Second)
	if late <= early {
		t.Fatalf("xmit_data_64 must grow: early=%d late=%d", early, late)
	}
}

func TestGenerator_ConstantFamilyDoesNotGrow(t *testing.T) {
	gen := g()
	e := find(t, "VL15_dropped")
	early := gen.Value(0, e, 1*time.Second)
	late := gen.Value(0, e, 1*time.Hour)
	if early != late {
		t.Fatalf("FamilyConstant must not grow: %d -> %d", early, late)
	}
}

func TestGenerator_Width32Truncates(t *testing.T) {
	gen := g()
	e := find(t, "port_xmit_data") // 32-bit legacy
	v := gen.Value(0, e, time.Hour)
	if v > 0xffffffff {
		t.Fatalf("32-bit counter exceeded uint32 range: %d", v)
	}
}

func TestGenerator_Width64DoesNotTruncate(t *testing.T) {
	gen := g()
	e := find(t, "port_xmit_data_64")
	v := gen.Value(0, e, time.Hour)
	if v <= 0xffffffff {
		t.Fatalf("expected 64-bit counter > uint32 at 400Gbps*1h, got %d", v)
	}
}

func TestGenerator_PacketsRelatesToData(t *testing.T) {
	gen := g()
	data := gen.Value(0, find(t, "port_xmit_data_64"), 60*time.Second)
	pkts := gen.Value(0, find(t, "port_xmit_packets_64"), 60*time.Second)
	if pkts >= data {
		t.Fatalf("packets (%d) should be much smaller than data words (%d)", pkts, data)
	}
}

func TestGenerator_ErrorFamilyIsLow(t *testing.T) {
	gen := g()
	e := find(t, "symbol_error")
	seed := gen.Value(0, e, 0)
	after := gen.Value(0, e, 60*time.Second)
	if after-seed > 10 {
		t.Fatalf("error counter grew too fast: %d in 60s", after-seed)
	}
}
