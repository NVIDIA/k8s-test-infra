// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"fmt"
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
)

func TestServer_LoopbackOpenSendRecv(t *testing.T) {
	dir := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	if err := render.Render(render.Options{IB: ib, GPUCount: 2, NodeName: "node-a", Output: dir}); err != nil {
		t.Fatal(err)
	}
	// Short path under /tmp: macOS limits unix socket paths; sandbox may block $TMPDIR binds.
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir}, nil)
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
	if openResp.Handle == 0 || openResp.Error != "" {
		t.Fatalf("open: %+v", openResp)
	}

	ports, err := sysfs.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var advert protocol.PortAdvert
	for _, p := range ports {
		if p.CAName == "mlx5_0" {
			advert = p
			break
		}
	}
	if advert.CAName == "" {
		t.Fatalf("mlx5_0 not in %+v", ports)
	}
	sendMad := madtest.PingMAD(advert)
	if err := protocol.WriteMessage(conn, protocol.TypeSend, protocol.SendReq{
		Handle: openResp.Handle,
		MAD:    sendMad,
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
		Handle:    openResp.Handle,
		TimeoutMS: 500,
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
	if recvResp.Timeout || len(recvResp.MAD) == 0 {
		t.Fatalf("recv: %+v", recvResp)
	}
	if recvResp.MAD[umadMADOffset+ibMADMethodOff]&0x80 == 0 {
		t.Fatal("expected response method bit set on echoed MAD")
	}

	if err := protocol.WriteMessage(conn, protocol.TypeClose, protocol.CloseReq{Handle: openResp.Handle}); err != nil {
		t.Fatal(err)
	}
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var closeResp protocol.CloseResp
	if err := protocol.DecodeBody(env, &closeResp); err != nil {
		t.Fatal(err)
	}
	if !closeResp.OK {
		t.Fatalf("close: %+v", closeResp)
	}

	cancel()
}

func TestServer_handleClose_unknownHandle(t *testing.T) {
	dir := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 1, NodeName: "n", Output: dir,
	}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("mock-ib-%d-close.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sock) })
	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir}, nil)
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

	if err := protocol.WriteMessage(conn, protocol.TypeClose, protocol.CloseReq{Handle: 9999}); err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		t.Fatal(err)
	}
	var closeResp protocol.CloseResp
	if err := protocol.DecodeBody(env, &closeResp); err != nil {
		t.Fatal(err)
	}
	if closeResp.OK {
		t.Fatalf("expected close error, got %+v", closeResp)
	}
	cancel()
}

func waitForSocket(t *testing.T, path string, errCh <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("server exited early: %v", err)
			}
		default:
		}
		c, err := net.Dial("unix", path)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket %s not ready", path)
}

