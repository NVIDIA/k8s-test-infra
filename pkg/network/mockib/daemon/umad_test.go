// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
	"github.com/stretchr/testify/require"
)

func TestDestLID(t *testing.T) {
	umad := make([]byte, 64)
	binary.BigEndian.PutUint16(umad[umadLIDOffset:], 0x0300)
	lid, ok := destLID(umad)
	require.True(t, ok, "got 0x%04x ok=%v", lid, ok)
	require.Equal(t, uint16(0x0300), lid, "got 0x%04x ok=%v", lid, ok)
}

func TestDestGID_and_PortGUID(t *testing.T) {
	umad := make([]byte, 72)
	binary.LittleEndian.PutUint32(umad[umadGRHPresent:], 1)
	gid.ParseInto(umad[umadGIDOffset:umadGIDOffset+16], "fe80:0000:0000:0000:a088:c203:00ab:2001")
	g, ok := destGID(umad)
	require.True(t, ok, "destGID")
	require.NotEmpty(t, g, "destGID")
	pg, ok := destPortGUID(umad)
	require.True(t, ok, "destPortGUID: got %q ok=%v", pg, ok)
	require.Equal(t, "a088:c203:00ab:2001", pg, "destPortGUID: got %q ok=%v", pg, ok)
}
