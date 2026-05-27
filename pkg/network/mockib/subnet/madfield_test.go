// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
)

func TestBitsoffs_PortInfoLid(t *testing.T) {
	// IB spec offsets are transformed by libibmad fields.c BITSOFFS macro.
	if got, want := Bitsoffs(128, 16), 144; got != want {
		t.Fatalf("Bitsoffs(128,16): got %d want %d", got, want)
	}
	if got, want := Bitsoffs(144, 16), 128; got != want {
		t.Fatalf("Bitsoffs(144,16): got %d want %d", got, want)
	}
}

func TestSetFieldPortInfoLinkUp(t *testing.T) {
	pl := make([]byte, 64)
	SetField64(pl, 64, defaultGidPrefix)
	SetFieldSpec(pl, 128, 16, 0x0dc0)
	SetFieldSpec(pl, 144, 16, defaultSMLID)
	SetFieldSpec(pl, 224, 8, 1)
	SetFieldSpec(pl, 232, 8, linkWidth4X)
	SetFieldSpec(pl, 240, 8, linkWidth4X)
	SetFieldSpec(pl, 248, 8, linkWidth4X)
	SetFieldSpec(pl, 256, 4, linkSpeed10G)
	SetFieldSpec(pl, 260, 4, portStateActive)
	SetFieldSpec(pl, 264, 4, portPhysStateLinkUp)
	SetFieldSpec(pl, 280, 4, linkSpeed10G)
	SetFieldSpec(pl, 284, 4, linkSpeed10G)

	cases := []struct {
		specOff, len int
		want         uint32
	}{
		{128, 16, 0x0dc0},
		{224, 8, 1},
		{248, 8, linkWidth4X},
		{260, 4, portStateActive},
		{264, 4, portPhysStateLinkUp},
		{280, 4, linkSpeed10G},
	}
	for _, c := range cases {
		if got := GetFieldSpec(pl, c.specOff, c.len); got != c.want {
			t.Fatalf("field spec@%d len=%d: got %#x want %#x", c.specOff, c.len, got, c.want)
		}
	}
}

func TestFillNodeInfo(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	fillNodeInfo(mad, fabricPort(0x101), 0)
	pl := mad[ibSMPDataOff:]

	if got := GetFieldSpec(pl, 16, 8); got != nodeTypeCA {
		t.Fatalf("node type: got %d want %d", got, nodeTypeCA)
	}
	if got := GetFieldSpec(pl, 288, 8); got != 1 {
		t.Fatalf("local port: got %d want 1", got)
	}
}

func TestFillPortInfo(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	fillPortInfo(mad, fabricPort(0x101), 1)
	pl := mad[ibSMPDataOff:]

	if got := GetFieldSpec(pl, 264, 4); got != portPhysStateLinkUp {
		t.Fatalf("phys state: got %d want %d", got, portPhysStateLinkUp)
	}
	if got := GetFieldSpec(pl, 248, 8); got != linkWidth4X {
		t.Fatalf("link width: got %d want %d", got, linkWidth4X)
	}
	if got := GetFieldSpec(pl, 128, 16); got != 0x101 {
		t.Fatalf("lid: got %#x want 0x101", got)
	}
}

func fabricPort(lid uint16) fabric.Port {
	return fabric.Port{
		PortGUID: "a088:c203:00ab:0001",
		NodeGUID: "a088:c203:00ab:0000",
		LID:      lid,
		CAName:   "mlx5_0",
	}
}
