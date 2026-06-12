// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon/madtest"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
	"github.com/stretchr/testify/require"
)

func TestSAPathQuery_LocalPort(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}))
	ports, err := sysfs.Scan(dir)
	require.NoError(t, err)
	srv := &Server{
		localPorts: ports,
		loopback:   NewLoopback(ports),
		registry:   registry.New(),
	}
	dgid := gidBytesForPort(t, ports[0].DefaultGID)
	send := madtest.SAPathQueryMAD(dgid)
	h := &portHandle{}
	require.True(t, srv.trySAPathQuery(h, send), "expected SA path query handled")
	require.Len(t, h.recvQ, 1, "recvQ len")
	dlidOff, _ := pathRecordDLIDOffset(h.recvQ[0][umadMADOffset:])
	gotLID := binary.BigEndian.Uint16(h.recvQ[0][umadMADOffset+dlidOff:])
	require.Equal(t, ports[0].LID, gotLID, "dlid")
	resp := h.recvQ[0][umadMADOffset:]
	require.NotZero(t, resp[20]&0x80, "SA GET response bit not set on method byte: 0x%02x", resp[20])
	// libibmad _do_madrpc matches TRID at MAD bytes 8-15; must not be corrupted by method scan.
	require.Equal(t, send[umadMADOffset+8:umadMADOffset+16], resp[8:16], "TRID bytes 8-15")
}

func TestSAPathQuery_RemoteViaRegistry(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dirA,
	}))
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-b", Output: dirB,
	}))
	portsA, _ := sysfs.Scan(dirA)
	portsB, _ := sysfs.Scan(dirB)

	client := &Server{
		localPorts: portsB,
		loopback:   NewLoopback(portsB),
		registry:   registry.New(),
	}
	client.registry.Register(portsA[0].PortGUID, registry.Peer{
		PodIP: "10.0.0.1", NodeName: "node-a", LID: portsA[0].LID,
	})

	dgid := gidBytesForPort(t, portsA[0].DefaultGID)
	send := madtest.SAPathQueryMAD(dgid)
	h := &portHandle{}
	require.True(t, client.trySAPathQuery(h, send), "expected remote SA path query handled")
	dlidOff, _ := pathRecordDLIDOffset(h.recvQ[0][umadMADOffset:])
	gotLID := binary.BigEndian.Uint16(h.recvQ[0][umadMADOffset+dlidOff:])
	require.Equal(t, portsA[0].LID, gotLID, "dlid")
}

func gidBytesForPort(t *testing.T, gidStr string) []byte {
	t.Helper()
	var b [16]byte
	gid.ParseInto(b[:], gidStr)
	return b[:]
}
