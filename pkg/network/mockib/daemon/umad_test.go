// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
)

func TestDestLID(t *testing.T) {
	umad := make([]byte, 64)
	binary.BigEndian.PutUint16(umad[umadLIDOffset:], 0x0300)
	lid, ok := destLID(umad)
	if !ok || lid != 0x0300 {
		t.Fatalf("got 0x%04x ok=%v", lid, ok)
	}
}

func TestDestGID_and_PortGUID(t *testing.T) {
	umad := make([]byte, 72)
	binary.LittleEndian.PutUint32(umad[umadGRHPresent:], 1)
	gid.ParseInto(umad[umadGIDOffset:umadGIDOffset+16], "fe80:0000:0000:0000:a088:c203:00ab:2001")
	g, ok := destGID(umad)
	if !ok || g == "" {
		t.Fatal("destGID")
	}
	pg, ok := destPortGUID(umad)
	if !ok || pg != "a088:c203:00ab:2001" {
		t.Fatalf("destPortGUID: got %q ok=%v", pg, ok)
	}
}
