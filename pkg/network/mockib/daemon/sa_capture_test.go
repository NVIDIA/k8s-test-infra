// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
	"github.com/stretchr/testify/require"
)

// Built from ibping -G LIBIBMAD_DEBUG_LEVEL=3 xdump (MAD payload rows at umad offset 64).
const ibpingSAPathSendHex = "" +
	"00000000000000000000000000000000" +
	"00000000000000006262c4eb00350000" +
	"00000000000000000000000000000000" +
	"0000000000000000000000000000000c" +
	"0000000000000000fe80000000000000" +
	"a088c20300ab2001fe80000000000000" +
	"a088c20300ab56040000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000"

func TestSAPathQuery_IbpingCapture(t *testing.T) {
	madPayload, err := hex.DecodeString(ibpingSAPathSendHex)
	require.NoError(t, err)
	require.Len(t, madPayload, 256, "payload len")
	umad := make([]byte, umadMADOffset+len(madPayload))
	copy(umad[umadMADOffset:], madPayload)
	binary.BigEndian.PutUint16(umad[umadLIDOffset:], 0x0001)

	require.True(t, isSAPathRecordGet(umad), "isSAPathRecordGet: want true for captured ibping SA GET")

	// The recorded SA GET targets port GUID a088:c203:00ab:2001 (the DGID in
	// the hex above). Register that exact GUID so the test pins SA PathRecord
	// resolution independently of the renderer's GUID encoding — the client's
	// own (rendered) ports are a different node, so resolution falls through
	// to the registry.
	const capturedTargetGUID = "a088:c203:00ab:2001"
	const capturedTargetLID uint16 = 0x0201

	dirB := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "nvml-mock-demo-worker2", Output: dirB,
	}))
	portsB, _ := sysfs.Scan(dirB)

	client := &Server{
		localPorts: portsB,
		loopback:   NewLoopback(portsB),
		registry:   registry.New(),
	}
	client.registry.Register(capturedTargetGUID, registry.Peer{
		PodIP: "10.0.0.1", NodeName: "node-a", LID: capturedTargetLID,
	})

	h := &portHandle{}
	require.True(t, client.trySAPathQuery(h, umad), "trySAPathQuery: want true")
	require.Len(t, h.recvQ, 1, "recvQ len")
	dlidOff, _ := pathRecordDLIDOffset(h.recvQ[0][umadMADOffset:])
	gotLID := binary.BigEndian.Uint16(h.recvQ[0][umadMADOffset+dlidOff:])
	require.Equal(t, capturedTargetLID, gotLID, "dlid")
	mad := h.recvQ[0][umadMADOffset:]
	require.Equal(t, umad[umadMADOffset+8:umadMADOffset+16], mad[8:16], "TRID bytes 8-15")
	require.True(t, saMethodResponseSet(mad), "expected GET response bit on SA method byte")
}
