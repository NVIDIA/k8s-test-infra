// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package subnet

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/fabric"
	"github.com/stretchr/testify/require"
)

func TestBitsoffs_PortInfoLid(t *testing.T) {
	// IB spec offsets are transformed by libibmad fields.c BITSOFFS macro.
	require.Equal(t, 144, Bitsoffs(128, 16), "Bitsoffs(128,16)")
	require.Equal(t, 128, Bitsoffs(144, 16), "Bitsoffs(144,16)")
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
		require.Equal(t, c.want, GetFieldSpec(pl, c.specOff, c.len), "field spec@%d len=%d", c.specOff, c.len)
	}
}

func TestFillNodeInfo(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	fillNodeInfo(mad, fabricPort(0x101), 0)
	pl := mad[ibSMPDataOff:]

	require.Equal(t, uint32(nodeTypeCA), GetFieldSpec(pl, 16, 8), "node type")
	require.Equal(t, uint32(1), GetFieldSpec(pl, 288, 8), "local port")
}

func TestFillPortInfo(t *testing.T) {
	mad := make([]byte, ibSMPDataOff+64)
	fillPortInfo(mad, fabricPort(0x101), 1)
	pl := mad[ibSMPDataOff:]

	require.Equal(t, uint32(portPhysStateLinkUp), GetFieldSpec(pl, 264, 4), "phys state")
	require.Equal(t, uint32(linkWidth4X), GetFieldSpec(pl, 248, 8), "link width")
	require.Equal(t, uint32(0x101), GetFieldSpec(pl, 128, 16), "lid")
}

func fabricPort(lid uint16) fabric.Port {
	return fabric.Port{
		PortGUID: "a088:c203:00ab:0001",
		NodeGUID: "a088:c203:00ab:0000",
		LID:      lid,
		CAName:   "mlx5_0",
	}
}
