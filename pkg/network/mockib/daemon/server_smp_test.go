// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/subnet"
	"github.com/stretchr/testify/require"
)

func TestServer_SMPPortInfoSelfResolveShort(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}))
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir, Fabric: true}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	waitForSocket(t, sock, errCh)

	conn, err := net.Dial("unix", sock)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeOpen, protocol.OpenReq{CAName: "mlx5_0", Port: 1}))
	var env protocol.Envelope
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var openResp protocol.OpenResp
	require.NoError(t, protocol.DecodeBody(env, &openResp))
	require.NotZero(t, openResp.Handle, "open: %+v", openResp)

	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]
	// Wire-format SMI-direct PORT_INFO GET (IBA §13.4): BaseVer | MgmtClass |
	// ClassVer | Method, with AttributeID as a BE16 at byte 16.
	mad[0] = 0x01 // BaseVersion
	mad[1] = 0x81 // MgmtClass = SMI Direct Route
	mad[2] = 0x01 // ClassVersion
	mad[3] = 0x01 // Method = Get
	binary.BigEndian.PutUint16(mad[16:18], 0x0015)
	subnet.SetField(mad, 32, 8, 0)

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{
		Handle: openResp.Handle,
		MAD:    send,
	}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var sendResp protocol.SendResp
	require.NoError(t, protocol.DecodeBody(env, &sendResp))
	require.True(t, sendResp.OK, "send: %+v", sendResp)

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeRecv, protocol.RecvReq{
		Handle: openResp.Handle, TimeoutMS: 500,
	}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var recvResp protocol.RecvResp
	require.NoError(t, protocol.DecodeBody(env, &recvResp))
	require.False(t, recvResp.Timeout, "recv: timeout=%v len=%d", recvResp.Timeout, len(recvResp.MAD))
	require.GreaterOrEqual(t, len(recvResp.MAD), umadMADOffset+128, "recv: timeout=%v len=%d", recvResp.Timeout, len(recvResp.MAD))
	pl := recvResp.MAD[umadMADOffset+64:]
	lid := subnet.GetFieldSpec(pl, 128, 16)
	phys := subnet.GetFieldSpec(pl, 264, 4)
	require.NotContains(t, []uint32{0, 1}, lid, "base lid: got %#x (likely echo/SMLid, not synthesized)", lid)
	require.Equal(t, uint32(5), phys, "phys state: want 5 (LINKUP)")
}

func TestServer_SMPNodeInfoThenPortInfo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}))
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir, Fabric: true}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	waitForSocket(t, sock, errCh)

	conn, err := net.Dial("unix", sock)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	var env protocol.Envelope
	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeOpen, protocol.OpenReq{CAName: "mlx5_0", Port: 1}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var openResp protocol.OpenResp
	require.NoError(t, protocol.DecodeBody(env, &openResp))
	require.NotZero(t, openResp.Handle, "open: %+v", openResp)
	handle := openResp.Handle

	smpSendRecv := func(attr uint16) []byte {
		t.Helper()
		send := make([]byte, umadMADOffset+64)
		mad := send[umadMADOffset:]
		// Wire-format SMI-direct GET (IBA §13.4): BaseVer | MgmtClass |
		// ClassVer | Method, with AttributeID as a BE16 at byte 16.
		mad[0] = 0x01 // BaseVersion
		mad[1] = 0x81 // MgmtClass = SMI Direct Route
		mad[2] = 0x01 // ClassVersion
		mad[3] = 0x01 // Method = Get
		binary.BigEndian.PutUint16(mad[16:18], attr)
		require.NoError(t, protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{Handle: handle, MAD: send}))
		require.NoError(t, protocol.ReadEnvelope(conn, &env))
		var sendResp protocol.SendResp
		require.NoError(t, protocol.DecodeBody(env, &sendResp))
		require.True(t, sendResp.OK, "send: %+v", sendResp)
		require.NoError(t, protocol.WriteMessage(conn, protocol.TypeRecv, protocol.RecvReq{Handle: handle, TimeoutMS: 500}))
		require.NoError(t, protocol.ReadEnvelope(conn, &env))
		var recvResp protocol.RecvResp
		require.NoError(t, protocol.DecodeBody(env, &recvResp))
		require.False(t, recvResp.Timeout, "recv: %+v", recvResp)
		require.NotZero(t, len(recvResp.MAD), "recv: %+v", recvResp)
		return recvResp.MAD
	}

	_ = smpSendRecv(0x0011)
	port := smpSendRecv(0x0015)
	pl := port[umadMADOffset+64:]
	require.Equal(t, uint32(5), subnet.GetFieldSpec(pl, 264, 4), "PORT_INFO phys: want 5")
	require.NotContains(t, []uint32{0, 1}, subnet.GetFieldSpec(pl, 128, 16), "PORT_INFO lid")
}
