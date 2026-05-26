// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"log"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon/madtest"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

func TestFabric_RegisterAndPingHandler(t *testing.T) {
	dir := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}); err != nil {
		t.Fatal(err)
	}
	ports, err := sysfs.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		cfg:        Config{TCPPort: 0, Fabric: true},
		localPorts: ports,
		registry:   registry.New(),
		podIP:      "10.0.0.1",
		nodeName:   "node-a",
		loopback:   NewLoopback(ports),
		log:        log.Default(),
		handles:    make(map[int]*portHandle),
	}
	ln := startTestFabricListener(t, srv)
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	remotePorts := []protocol.PortAdvert{{
		PortGUID: "a088:c203:00ab:00ff",
		CAName:   "mlx5_0",
		Port:     1,
		LID:      0x0200,
	}}
	if err := protocol.WriteMessage(conn, protocol.TypeRegister, protocol.RegisterBody{
		NodeName: "node-b",
		PodIP:    "10.0.0.2",
		Ports:    remotePorts,
	}); err != nil {
		t.Fatal(err)
	}

	if err := protocol.WriteMessage(conn, protocol.TypePing, protocol.PingBody{
		DstPortGUID: ports[0].PortGUID,
		Seq:         42,
		ClientTS:    time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.TypePong {
		t.Fatalf("type: got %q want %q", env.Type, protocol.TypePong)
	}
	var pong protocol.PongBody
	if err := protocol.DecodeBody(env, &pong); err != nil {
		t.Fatal(err)
	}
	if pong.Seq != 42 {
		t.Fatalf("pong seq: got %d want 42", pong.Seq)
	}

	peer, ok := srv.registry.Lookup("a088:c203:00ab:00ff")
	if !ok || peer.PodIP != "10.0.0.2" {
		t.Fatalf("register lookup failed: %+v ok=%v", peer, ok)
	}
}

func TestFabric_RemoteSendForwardsPing(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dirA,
	}); err != nil {
		t.Fatal(err)
	}
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-b", Output: dirB,
	}); err != nil {
		t.Fatal(err)
	}
	portsA, err := sysfs.Scan(dirA)
	if err != nil {
		t.Fatal(err)
	}
	portsB, err := sysfs.Scan(dirB)
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		cfg:        Config{TCPPort: 0, Fabric: true},
		localPorts: portsA,
		registry:   registry.New(),
		podIP:      "127.0.0.1",
		nodeName:   "node-a",
		loopback:   NewLoopback(portsA),
		log:        log.Default(),
		handles:    make(map[int]*portHandle),
	}
	ln := startTestFabricListener(t, server)
	defer ln.Close()

	client := &Server{
		cfg:        Config{TCPPort: server.cfg.TCPPort, Fabric: true},
		localPorts: portsB,
		registry:   registry.New(),
		podIP:      "10.0.0.2",
		nodeName:   "node-b",
		loopback:   NewLoopback(portsB),
		log:        log.Default(),
		handles:    make(map[int]*portHandle),
	}
	client.registry.Register(portsA[0].PortGUID, registry.Peer{
		PodIP:    "127.0.0.1",
		NodeName: "node-a",
		CAName:   portsA[0].CAName,
		Port:     portsA[0].Port,
		LID:      portsA[0].LID,
	})

	h := &portHandle{caName: portsB[0].CAName, port: portsB[0].Port}
	sendMad := madtest.PingMAD(portsA[0])
	if !client.tryFabricSend(h, sendMad) {
		t.Fatal("tryFabricSend: expected remote ping success")
	}
	if len(h.recvQ) != 1 {
		t.Fatalf("recvQ len: got %d want 1", len(h.recvQ))
	}
	if h.recvQ[0][umadMADOffset+ibMADMethodOff]&0x80 == 0 {
		t.Fatal("expected response method bit set on synthesized MAD")
	}
}

func startTestFabricListener(t *testing.T, srv *Server) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.TCPPort = port
	go srv.acceptFabric(context.Background(), ln)
	return ln
}
