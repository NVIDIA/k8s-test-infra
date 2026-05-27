// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/subnet"
)

func startServerWithCounters(t *testing.T) (*Server, net.Conn, int) {
	t.Helper()
	dir := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true, RateGbps: 400, HCAsPerGPU: 1}, GPUCount: 1, NodeName: "pma-test", Output: dir,
	}); err != nil {
		t.Fatal(err)
	}
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen := counters.Generator{NodeID: 0xab, RateGbps: 400}
	epochs := counters.NewEpochs(time.Now().Add(-time.Minute))
	srv.SetCounters(gen, epochs)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	waitForSocket(t, sock, errCh)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := protocol.WriteMessage(conn, protocol.TypeOpen, protocol.OpenReq{CAName: "mlx5_0", Port: 1}); err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var openResp protocol.OpenResp
	if err := protocol.DecodeBody(env, &openResp); err != nil || openResp.Handle == 0 {
		t.Fatalf("open: %+v err=%v", openResp, err)
	}
	return srv, conn, openResp.Handle
}

func sendRecvPMA(t *testing.T, conn net.Conn, handle int, umad []byte) []byte {
	t.Helper()
	if err := protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{Handle: handle, MAD: umad}); err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var sendResp protocol.SendResp
	if err := protocol.DecodeBody(env, &sendResp); err != nil || !sendResp.OK {
		t.Fatalf("send: %+v err=%v", sendResp, err)
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

// TestServer_PMAExtGetSocket exercises the full dispatch chain: client
// writes a PMA PortCountersExt Get over the unix socket,
// server.handleSend routes via IsPMASend, TrySynthesizePMA fills the
// 64-bit fields, client recvs and sees non-zero traffic counters.
// (Test name kept short to stay under macOS' 104-byte sun_path limit.)
func TestServer_PMAExtGetSocket(t *testing.T) {
	_, conn, handle := startServerWithCounters(t)

	req := buildPMAUMAD(0x04, 0x01, PMAAttrPortCountersExt)
	pl := req[56+64:]
	subnet.SetFieldSpec(pl, 0, 8, 1)
	subnet.SetFieldSpec(pl, 16, 16, 0xffff)
	resp := sendRecvPMA(t, conn, handle, req)

	if resp[56+3] != 0x81 {
		t.Fatalf("PMA response method = 0x%02x, want 0x81", resp[56+3])
	}
	if v := getPMAFieldSpec(t, resp, 0, 8); v != 1 {
		t.Fatalf("PortSelect echo = %d, want 1", v)
	}
	if v := getPMAField64(t, resp, 64); v == 0 {
		t.Fatal("port_xmit_data_64 must be non-zero")
	}
}

// TestServer_PMAResetSocket validates that a Set ClearCounters issued
// over the socket reduces subsequent Get values (epoch reset shared
// with the writer).
func TestServer_PMAResetSocket(t *testing.T) {
	_, conn, handle := startServerWithCounters(t)

	get := buildPMAUMAD(0x04, 0x01, PMAAttrPortCountersExt)
	pl := get[56+64:]
	subnet.SetFieldSpec(pl, 0, 8, 1)
	subnet.SetFieldSpec(pl, 16, 16, 0xffff)
	before := getPMAField64(t, sendRecvPMA(t, conn, handle, get), 64)
	if before == 0 {
		t.Fatal("pre-reset value must be non-zero")
	}

	set := buildPMAUMAD(0x04, 0x02, PMAAttrPortCountersExt)
	sp := set[56+64:]
	subnet.SetFieldSpec(sp, 0, 8, 1)
	subnet.SetFieldSpec(sp, 16, 16, 0xffff)
	setResp := sendRecvPMA(t, conn, handle, set)
	if setResp[56+3] != 0x82 {
		t.Fatalf("Set response method = 0x%02x, want 0x82", setResp[56+3])
	}

	after := getPMAField64(t, sendRecvPMA(t, conn, handle, get), 64)
	if after >= before {
		t.Fatalf("expected Set to reduce counter: before=%d after=%d", before, after)
	}
}
