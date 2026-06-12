// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	got := Normalize(" FE80::1 ")
	require.Equal(t, "fe80::1", got)
}

func TestPortGUIDFromBytes(t *testing.T) {
	var b [16]byte
	copy(b[8:], []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x20, 0x01})
	got := PortGUIDFromBytes(b[:])
	want := "a088:c203:00ab:2001"
	require.Equal(t, want, got)
}

func TestParseInto(t *testing.T) {
	var dst [16]byte
	ParseInto(dst[:], "fe80:0000:0000:0000:a088:c203:00ab:2001")
	require.Equal(t, "fe80:0000:0000:0000:a088:c203:00ab:2001", Format(dst[:]), "round-trip")
}
