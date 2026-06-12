// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/stretchr/testify/require"
)

// TestApplyRegister_LogsOnlyOnChange pins REGISTER log volume: peers
// re-register every 2s, so an unchanged registration must stay silent
// (~86k lines/day per port otherwise) while the first registration and any
// change keep the existing per-port log line for kubectl-logs debuggability.
func TestApplyRegister_LogsOnlyOnChange(t *testing.T) {
	var buf bytes.Buffer
	srv := &Server{registry: registry.New(), log: log.New(&buf, "", 0)}
	body := protocol.RegisterBody{
		NodeName: "node-b",
		PodIP:    "10.0.0.2",
		Ports: []protocol.PortAdvert{
			{PortGUID: "a088:c203:00ab:2001", LID: 0x0300, CAName: "mlx5_0", Port: 1},
			{PortGUID: "a088:c203:00ab:2002", LID: 0x0301, CAName: "mlx5_1", Port: 1},
		},
	}

	srv.applyRegister(body)
	require.Equal(t, 2, strings.Count(buf.String(), "register from"),
		"first REGISTER must log every port:\n%s", buf.String())
	// First-registration log content must stay identical (CI greps depend on it).
	require.Contains(t, buf.String(),
		`mock-ib: register from podIP=10.0.0.2 node="node-b" ca=mlx5_0 port=1 lid=0x0300 port_guid=a088:c203:00ab:2001`)

	// Identical 2s re-register: no new lines.
	srv.applyRegister(body)
	require.Equal(t, 2, strings.Count(buf.String(), "register from"),
		"unchanged re-register must not log:\n%s", buf.String())

	// LID change on one port: exactly one new line, for the changed port.
	body.Ports[1].LID = 0x0999
	srv.applyRegister(body)
	require.Equal(t, 3, strings.Count(buf.String(), "register from"),
		"changed port must log exactly once:\n%s", buf.String())
	require.Contains(t, buf.String(), "lid=0x0999")
}

// TestRegisterWithPeers_CancelledCtxMakesNoDials pins ctx handling in the
// peer register sweep: registerWithPeers checks ctx between peers (post-SIGTERM
// the sequential loop must stop, not keep dialing through the list), so with
// ctx already canceled before the sweep, not a single peer may be dialed.
func TestRegisterWithPeers_CancelledCtxMakesNoDials(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var accepts atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = c.Close()
		}
	}()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	tcpPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	t.Setenv(EnvMockIBPeers, "127.0.0.1")
	srv := &Server{
		cfg:            Config{TCPPort: tcpPort},
		podIP:          "10.255.255.1", // must differ from the peer so it is not skipped as self
		log:            log.New(io.Discard, "", 0),
		registerWarned: make(map[string]struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.registerWithPeers(ctx)

	// Give a stray dial time to reach the accept loop before judging.
	time.Sleep(300 * time.Millisecond)
	require.Zero(t, accepts.Load(), "canceled ctx must stop the sweep before any peer dial")
}

// TestWriteRegister_StalledPeerTimesOut pins the I/O deadline on outbound
// REGISTER writes. A peer that accepts the TCP connection but never reads
// (wedged pod, half-open conn) must not hang the write forever:
// registerWithPeersLoop is sequential, so one stuck write silently stops
// re-registration to every peer for the rest of the pod's life.
//
// The write is exercised through an unbuffered net.Pipe rather than a real
// TCP socket: kernel sockets absorb writes into tunable buffers (an SO_RCVBUF
// variant of this test passed on darwin and failed on linux runners), while a
// pipe write blocks until the far side reads, so the deadline path fires
// deterministically on every platform.
func TestWriteRegister_StalledPeerTimesOut(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	// The server side never reads.

	start := time.Now()
	err := writeRegister(client, 50*time.Millisecond, protocol.RegisterBody{
		NodeName: "node-b",
		PodIP:    "10.0.0.2",
	})
	require.Error(t, err, "write to a peer that never reads must fail once the deadline expires")
	require.ErrorIs(t, err, os.ErrDeadlineExceeded, "want deadline error, got: %v", err)
	require.Less(t, time.Since(start), 5*time.Second, "writeRegister must return at the configured deadline")
}

func TestServer_sendRegister(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 1, NodeName: "node-a", Output: dir,
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	tcpPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	srv, err := NewServer(Config{IBRoot: dir, TCPPort: tcpPort, Fabric: true}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.acceptFabric(ctx, ln)

	body := protocol.RegisterBody{
		PodIP:    "10.0.0.2",
		NodeName: "node-b",
		Ports: []protocol.PortAdvert{
			{PortGUID: "a088:c203:00ab:2001", LID: 0x0300, CAName: "mlx5_0", Port: 1},
		},
	}
	require.NoError(t, srv.sendRegister("127.0.0.1", body))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if peer, ok := srv.registry.Lookup("a088:c203:00ab:2001"); ok && peer.PodIP == "10.0.0.2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Fail(t, "peer not registered")
}
