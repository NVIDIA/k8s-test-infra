// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/protocol"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/registry"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestApplyRegister_LogsOnlyOnChange pins REGISTER log volume: peers
// re-register every 2s, so an unchanged registration must stay silent —
// otherwise a two-port node emits ~86k lines a day saying nothing changed.
func TestApplyRegister_LogsOnlyOnChange(t *testing.T) {
	logs := captureLogs(t)
	srv := &Server{log: newLogger(), registry: registry.New()}
	body := protocol.RegisterBody{
		NodeName: "node-b",
		PodIP:    "10.0.0.2",
		Ports: []protocol.PortAdvert{
			{PortGUID: "a088:c203:00ab:2001", LID: 0x0300, CAName: "mlx5_0", Port: 1},
			{PortGUID: "a088:c203:00ab:2002", LID: 0x0301, CAName: "mlx5_1", Port: 1},
		},
	}

	srv.applyRegister(body)
	require.Equal(t, 2, logs.FilterMessage("peer registered").Len(),
		"first REGISTER must log every port:\n%v", logs.All())
	require.Equal(t, "a088:c203:00ab:2001",
		logs.FilterMessage("peer registered").All()[0].ContextMap()["port_guid"])

	srv.applyRegister(body)
	require.Equal(t, 2, logs.FilterMessage("peer registered").Len(),
		"unchanged re-register must not log:\n%v", logs.All())

	body.Ports[1].LID = 0x0999
	srv.applyRegister(body)
	require.Equal(t, 3, logs.FilterMessage("peer registered").Len(),
		"changed port must log exactly once:\n%v", logs.All())
	require.Equal(t, "0x0999",
		logs.FilterMessage("peer registered").All()[2].ContextMap()["lid"])
}

// captureLogs redirects the global logger for one test. The daemon logs through
// zap.L() rather than an injected logger, so assertions on log output have to
// swap the global and restore it.
func captureLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(restore)
	return logs
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

	t.Setenv(envMockIBPeers, "127.0.0.1")
	srv := &Server{
		log:            newLogger(),
		cfg:            Config{TCPPort: tcpPort},
		podIP:          "10.255.255.1", // must differ from the peer so it is not skipped as self
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
	require.NoError(t, sysfs.Render(sysfs.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 1, NodeName: "node-a", RootDir: dir,
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	tcpPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	srv, err := NewServer(Config{IBRoot: dir, TCPPort: tcpPort, Fabric: true})
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
