// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"context"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
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

// TestServer_sendRegister_StalledPeerTimesOut pins the I/O deadline on
// outbound REGISTER connections. A peer that accepts the TCP connection but
// never reads (wedged pod, half-open conn) must not hang sendRegister forever:
// registerWithPeersLoop is sequential, so one stuck write silently stops
// re-registration to every peer for the rest of the pod's life.
//
// The register body is sized near the 1 MiB frame cap and the listener's
// receive buffer is pinned small (accepted sockets inherit SO_RCVBUF from the
// listening socket on Linux and macOS) so the kernel cannot absorb the whole
// frame and the write genuinely blocks until the deadline fires.
func TestServer_sendRegister_StalledPeerTimesOut(t *testing.T) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096)
		}); err != nil {
			return err
		}
		return serr
	}}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	tcpPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	// Accept and hold the connection open without ever reading from it.
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()
	t.Cleanup(func() {
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	})

	srv := &Server{cfg: Config{TCPPort: tcpPort}}
	body := protocol.RegisterBody{
		// ~1000 KiB of payload: above loopback socket buffering, below the
		// protocol.MaxFrameSize (1 MiB) frame cap.
		NodeName: strings.Repeat("n", 1000*1024),
		PodIP:    "10.0.0.2",
	}

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- srv.sendRegister("127.0.0.1", body) }()
	select {
	case err := <-done:
		require.Error(t, err, "write to a peer that never reads must fail once the deadline expires")
		require.ErrorIs(t, err, os.ErrDeadlineExceeded, "want deadline error, got: %v", err)
		require.Less(t, time.Since(start), 20*time.Second, "sendRegister must return around the 5s I/O deadline")
	case <-time.After(25 * time.Second):
		t.Fatal("sendRegister hung: no I/O deadline set on the fabric connection")
	}
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
