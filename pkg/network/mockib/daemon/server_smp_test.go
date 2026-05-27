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
)

func TestServer_SMPPortInfoSelfResolveShort(t *testing.T) {
	dir := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}); err != nil {
		t.Fatal(err)
	}
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir, Fabric: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	waitForSocket(t, sock, errCh)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if err := protocol.WriteMessage(conn, protocol.TypeOpen, protocol.OpenReq{CAName: "mlx5_0", Port: 1}); err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var openResp protocol.OpenResp
	if err := protocol.DecodeBody(env, &openResp); err != nil {
		t.Fatal(err)
	}
	if openResp.Handle == 0 {
		t.Fatalf("open: %+v", openResp)
	}

	send := make([]byte, umadMADOffset+64)
	mad := send[umadMADOffset:]
	hdr := make([]byte, 24)
	hdr[1] = 0x81
	hdr[3] = 0x01
	attr := uint16(0x0015)
	attr = (attr >> 8) | (attr << 8)
	binary.BigEndian.PutUint16(hdr[18:20], attr)
	for w := 0; w < 24; w += 4 {
		mad[w+0] = hdr[w+3]
		mad[w+1] = hdr[w+2]
		mad[w+2] = hdr[w+1]
		mad[w+3] = hdr[w+0]
	}
	subnet.SetField(mad, 32, 8, 0)

	if err := protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{
		Handle: openResp.Handle,
		MAD:    send,
	}); err != nil {
		t.Fatal(err)
	}
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var sendResp protocol.SendResp
	if err := protocol.DecodeBody(env, &sendResp); err != nil {
		t.Fatal(err)
	}
	if !sendResp.OK {
		t.Fatalf("send: %+v", sendResp)
	}

	if err := protocol.WriteMessage(conn, protocol.TypeRecv, protocol.RecvReq{
		Handle: openResp.Handle, TimeoutMS: 500,
	}); err != nil {
		t.Fatal(err)
	}
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var recvResp protocol.RecvResp
	if err := protocol.DecodeBody(env, &recvResp); err != nil {
		t.Fatal(err)
	}
	if recvResp.Timeout || len(recvResp.MAD) < umadMADOffset+128 {
		t.Fatalf("recv: timeout=%v len=%d", recvResp.Timeout, len(recvResp.MAD))
	}
	pl := recvResp.MAD[umadMADOffset+64:]
	lid := subnet.GetFieldSpec(pl, 128, 16)
	phys := subnet.GetFieldSpec(pl, 264, 4)
	if lid == 0 || lid == 1 {
		t.Fatalf("base lid: got %#x (likely echo/SMLid, not synthesized)", lid)
	}
	if phys != 5 {
		t.Fatalf("phys state: got %d want 5 (LINKUP)", phys)
	}
}

func TestServer_SMPNodeInfoThenPortInfo(t *testing.T) {
	dir := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}); err != nil {
		t.Fatal(err)
	}
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir, Fabric: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	waitForSocket(t, sock, errCh)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	var env protocol.Envelope
	if err := protocol.WriteMessage(conn, protocol.TypeOpen, protocol.OpenReq{CAName: "mlx5_0", Port: 1}); err != nil {
		t.Fatal(err)
	}
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var openResp protocol.OpenResp
	if err := protocol.DecodeBody(env, &openResp); err != nil || openResp.Handle == 0 {
		t.Fatalf("open: %+v err=%v", openResp, err)
	}
	handle := openResp.Handle

	smpSendRecv := func(attr uint16) []byte {
		t.Helper()
		send := make([]byte, umadMADOffset+64)
		mad := send[umadMADOffset:]
		hdr := make([]byte, 24)
		hdr[1] = 0x81
		hdr[3] = 0x01
		attrSw := (attr >> 8) | (attr << 8)
		binary.BigEndian.PutUint16(hdr[18:20], attrSw)
		for w := 0; w < 24; w += 4 {
			mad[w+0] = hdr[w+3]
			mad[w+1] = hdr[w+2]
			mad[w+2] = hdr[w+1]
			mad[w+3] = hdr[w+0]
		}
		if err := protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{Handle: handle, MAD: send}); err != nil {
			t.Fatal(err)
		}
		if err := protocol.ReadEnvelope(conn, &env); err != nil {
			t.Fatal(err)
		}
		var sendResp protocol.SendResp
		if err := protocol.DecodeBody(env, &sendResp); err != nil || !sendResp.OK {
			t.Fatalf("send: %+v", sendResp)
		}
		if err := protocol.WriteMessage(conn, protocol.TypeRecv, protocol.RecvReq{Handle: handle, TimeoutMS: 500}); err != nil {
			t.Fatal(err)
		}
		if err := protocol.ReadEnvelope(conn, &env); err != nil {
			t.Fatal(err)
		}
		var recvResp protocol.RecvResp
		if err := protocol.DecodeBody(env, &recvResp); err != nil {
			t.Fatal(err)
		}
		if recvResp.Timeout || len(recvResp.MAD) == 0 {
			t.Fatalf("recv: %+v", recvResp)
		}
		return recvResp.MAD
	}

	_ = smpSendRecv(0x0011)
	port := smpSendRecv(0x0015)
	pl := port[umadMADOffset+64:]
	if got := subnet.GetFieldSpec(pl, 264, 4); got != 5 {
		t.Fatalf("PORT_INFO phys: got %d want 5", got)
	}
	if got := subnet.GetFieldSpec(pl, 128, 16); got == 0 || got == 1 {
		t.Fatalf("PORT_INFO lid: got %#x", got)
	}
}
