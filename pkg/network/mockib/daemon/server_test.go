// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon/madtest"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
	"github.com/stretchr/testify/require"
)

func TestServer_LoopbackOpenSendRecv(t *testing.T) {
	dir := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	require.NoError(t, render.Render(render.Options{IB: ib, GPUCount: 2, NodeName: "node-a", Output: dir}))
	// Short path under /tmp: macOS limits unix socket paths; sandbox may block $TMPDIR binds.
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir}, nil)
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
	require.Empty(t, openResp.Error, "open: %+v", openResp)

	ports, err := sysfs.Scan(dir)
	require.NoError(t, err)
	var advert protocol.PortAdvert
	for _, p := range ports {
		if p.CAName == "mlx5_0" {
			advert = p
			break
		}
	}
	require.NotEmpty(t, advert.CAName, "mlx5_0 not in %+v", ports)
	sendMad := madtest.PingMAD(advert)
	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{
		Handle: openResp.Handle,
		MAD:    sendMad,
	}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var sendResp protocol.SendResp
	require.NoError(t, protocol.DecodeBody(env, &sendResp))
	require.True(t, sendResp.OK, "send: %+v", sendResp)

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeRecv, protocol.RecvReq{
		Handle:    openResp.Handle,
		TimeoutMS: 500,
	}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var recvResp protocol.RecvResp
	require.NoError(t, protocol.DecodeBody(env, &recvResp))
	require.False(t, recvResp.Timeout, "recv: %+v", recvResp)
	require.NotZero(t, len(recvResp.MAD), "recv: %+v", recvResp)
	require.NotZero(t, recvResp.MAD[umadMADOffset+ibMADMethodOff]&0x80, "expected response method bit set on echoed MAD")

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeClose, protocol.CloseReq{Handle: openResp.Handle}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var closeResp protocol.CloseResp
	require.NoError(t, protocol.DecodeBody(env, &closeResp))
	require.True(t, closeResp.OK, "close: %+v", closeResp)

	cancel()
}

func TestServer_handleSend_shortMADNoPanic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 1, NodeName: "n", Output: dir,
	}))
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("mock-ib-%d-short.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sock) })
	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir}, nil)
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

	// A truncated umad buffer (shorter than the 56-byte header) used to slice
	// umad[umadMADOffset:] out of range and panic the serveConn goroutine.
	// Now it must come back as an error and leave the daemon serving.
	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{
		Handle: openResp.Handle,
		MAD:    make([]byte, 10),
	}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var sendResp protocol.SendResp
	require.NoError(t, protocol.DecodeBody(env, &sendResp))
	require.False(t, sendResp.OK, "short send: got %+v, want error containing \"too short\"", sendResp)
	require.Contains(t, sendResp.Error, "too short", "short send: got %+v", sendResp)

	// The daemon must still answer on the same connection (goroutine survived).
	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeClose, protocol.CloseReq{Handle: openResp.Handle}))
	require.NoError(t, protocol.ReadEnvelope(conn, &env), "daemon stopped serving after short MAD")
	cancel()
}

func TestServer_handleClose_unknownHandle(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 1, NodeName: "n", Output: dir,
	}))
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("mock-ib-%d-close.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sock) })
	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	waitForSocket(t, sock, errCh)

	conn, err := net.Dial("unix", sock)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, protocol.WriteMessage(conn, protocol.TypeClose, protocol.CloseReq{Handle: 9999}))
	var env protocol.Envelope
	require.NoError(t, protocol.ReadEnvelope(conn, &env))
	var closeResp protocol.CloseResp
	require.NoError(t, protocol.DecodeBody(env, &closeResp))
	require.False(t, closeResp.OK, "expected close error, got %+v", closeResp)
	cancel()
}

// TestEffectiveRecvTimeout pins the clamp on the client-supplied recv timeout.
// timeout_ms comes straight off the Unix socket, so an absurd or hostile value
// must not let one recv RPC pin a poll loop (and its goroutine) for minutes.
func TestEffectiveRecvTimeout(t *testing.T) {
	tests := []struct {
		name string
		ms   int
		want time.Duration
	}{
		{"zero uses default", 0, time.Second},
		{"negative uses default", -100, time.Second},
		{"small value passes through", 500, 500 * time.Millisecond},
		{"exactly at cap", 60_000, 60 * time.Second},
		{"above cap clamps", 600_000, 60 * time.Second},
		{"overflow-sized clamps", math.MaxInt, 60 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, effectiveRecvTimeout(tc.ms))
		})
	}
}

// TestServer_handleRecv_EmptyQueueTimesOut pins that a small client timeout is
// still honored through the poll loop: an empty queue yields Timeout=true
// shortly after timeout_ms, not at the cap and not never.
func TestServer_handleRecv_EmptyQueueTimesOut(t *testing.T) {
	srv := &Server{handles: map[int]*portHandle{1: {caName: "mlx5_0", port: 1}}}
	clientEnd, serverEnd := net.Pipe()
	t.Cleanup(func() { _ = clientEnd.Close(); _ = serverEnd.Close() })

	done := make(chan error, 1)
	go func() {
		done <- srv.handleRecv(context.Background(), serverEnd, protocol.RecvReq{Handle: 1, TimeoutMS: 50})
	}()

	resp := readRecvResp(t, clientEnd, 5*time.Second)
	require.True(t, resp.Timeout, "empty queue must time out: %+v", resp)
	require.NoError(t, <-done)
}

// TestServer_handleRecv_CtxCancelReturnsTimeout pins shutdown behavior: when
// the server ctx is canceled mid-recv, handleRecv must return promptly (not
// poll out the remaining client timeout) and complete the in-flight RPC as a
// Timeout response — the client's normal no-data path — matching how the rest
// of the daemon treats ctx cancellation as an expected lifecycle event.
func TestServer_handleRecv_CtxCancelReturnsTimeout(t *testing.T) {
	srv := &Server{handles: map[int]*portHandle{1: {caName: "mlx5_0", port: 1}}}
	clientEnd, serverEnd := net.Pipe()
	t.Cleanup(func() { _ = clientEnd.Close(); _ = serverEnd.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		// 600s client timeout: without the ctx check this blocks for the full
		// 60s cap; with it, the response arrives right after cancel.
		done <- srv.handleRecv(ctx, serverEnd, protocol.RecvReq{Handle: 1, TimeoutMS: 600_000})
	}()
	time.AfterFunc(50*time.Millisecond, cancel)

	resp := readRecvResp(t, clientEnd, 5*time.Second)
	require.True(t, resp.Timeout, "shutdown recv must complete as a timeout: %+v", resp)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("handleRecv did not return after ctx cancellation")
	}
	require.Less(t, time.Since(start), 5*time.Second,
		"handleRecv must return promptly on ctx cancel, not at the recv timeout cap")
}

// readRecvResp reads one recv envelope from c, failing the test after timeout.
// net.Pipe writes are synchronous, so the read also unblocks handleRecv's
// response write.
func readRecvResp(t *testing.T, c net.Conn, timeout time.Duration) protocol.RecvResp {
	t.Helper()
	type result struct {
		env protocol.Envelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var env protocol.Envelope
		err := protocol.ReadEnvelope(c, &env)
		ch <- result{env, err}
	}()
	select {
	case r := <-ch:
		require.NoError(t, r.err)
		require.Equal(t, protocol.TypeRecv, r.env.Type)
		var resp protocol.RecvResp
		require.NoError(t, protocol.DecodeBody(r.env, &resp))
		return resp
	case <-time.After(timeout):
		t.Fatalf("no recv response within %v", timeout)
		return protocol.RecvResp{}
	}
}

func waitForSocket(t *testing.T, path string, errCh <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			require.True(t, err == nil || errors.Is(err, context.Canceled), "server exited early: %v", err)
		default:
		}
		c, err := net.Dial("unix", path)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Failf(t, "socket not ready", "socket %s not ready", path)
}
