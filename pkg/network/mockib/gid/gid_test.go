// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gid

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	if got := Normalize(" FE80::1 "); got != "fe80::1" {
		t.Fatalf("got %q", got)
	}
}

func TestPortGUIDFromBytes(t *testing.T) {
	var b [16]byte
	copy(b[8:], []byte{0xa0, 0x88, 0xc2, 0x03, 0x00, 0xab, 0x20, 0x01})
	got := PortGUIDFromBytes(b[:])
	want := "a088:c203:00ab:2001"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseInto(t *testing.T) {
	var dst [16]byte
	ParseInto(dst[:], "fe80:0000:0000:0000:a088:c203:00ab:2001")
	if Format(dst[:]) != "fe80:0000:0000:0000:a088:c203:00ab:2001" {
		t.Fatalf("round-trip: %s", Format(dst[:]))
	}
}
