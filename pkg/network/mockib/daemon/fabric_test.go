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

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon/madtest"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
	"github.com/stretchr/testify/require"
)

func TestFabric_RegisterAndPingHandler(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}))
	ports, err := sysfs.Scan(dir)
	require.NoError(t, err)

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
	defer func() { _ = ln.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	remotePorts := []protocol.PortAdvert{{
		PortGUID: "a088:c203:00ab:00ff",
		CAName:   "mlx5_0",
		Port:     1,
		LID:      0x0200,
	}}
	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeRegister, protocol.RegisterBody{
		NodeName: "node-b",
		PodIP:    "10.0.0.2",
		Ports:    remotePorts,
	}))

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypePing, protocol.PingBody{
		DstPortGUID: ports[0].PortGUID,
		Seq:         42,
		ClientTS:    time.Now().UnixNano(),
	}))
	var env protocol.Envelope
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	require.Equal(t, protocol.TypePong, env.Type, "type")
	var pong protocol.PongBody
	require.NoError(t, protocol.DecodeBody(env, &pong))
	require.Equal(t, uint32(42), pong.Seq, "pong seq")

	peer, ok := srv.registry.Lookup("a088:c203:00ab:00ff")
	require.True(t, ok, "register lookup failed: %+v", peer)
	require.Equal(t, "10.0.0.2", peer.PodIP, "register lookup failed: %+v", peer)
}

func TestFabric_RemoteSendForwardsPing(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dirA,
	}))
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-b", Output: dirB,
	}))
	portsA, err := sysfs.Scan(dirA)
	require.NoError(t, err)
	portsB, err := sysfs.Scan(dirB)
	require.NoError(t, err)

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
	defer func() { _ = ln.Close() }()

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
	require.True(t, client.tryFabricSend(h, sendMad), "tryFabricSend: expected remote ping success")
	require.Len(t, h.recvQ, 1, "recvQ len")
	require.NotZero(t, h.recvQ[0][umadMADOffset+ibMADMethodOff]&0x80, "expected response method bit set on synthesized MAD")
}

func startTestFabricListener(t *testing.T, srv *Server) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	srv.cfg.TCPPort = port
	go srv.acceptFabric(context.Background(), ln)
	return ln
}
