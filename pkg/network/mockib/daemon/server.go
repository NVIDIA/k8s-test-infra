// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements mock-ib Unix-socket UMAD loopback (phase 1).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
)

// Config holds mock-ib runtime options.
type Config struct {
	SocketPath string
	IBRoot     string
	TCPPort    int
	Fabric     bool
}

// Server serves libibmockumad over a Unix socket.
type Server struct {
	cfg      Config
	loopback *Loopback
	log      *log.Logger

	localPorts []protocol.PortAdvert
	registry   *registry.Registry
	podIP      string
	nodeName   string
	nextSeq    uint32

	nextHandleID int
	handles      map[int]*portHandle
	handlesMu    sync.Mutex

	// Fabric registration: suppress repeated "connection refused" while peers start.
	lastPeerRegisterOK int
	registerWarned     map[string]struct{}
	registerWarnedMu   sync.Mutex
}

// NewServer builds a server; local ports are loaded from ib-root sysfs.
func NewServer(cfg Config, logger *log.Logger) (*Server, error) {
	if logger == nil {
		logger = log.Default()
	}
	ports, err := sysfs.Scan(cfg.IBRoot)
	if err != nil {
		return nil, fmt.Errorf("scan ib-root %q: %w", cfg.IBRoot, err)
	}
	srv := &Server{
		cfg:        cfg,
		loopback:   NewLoopback(ports),
		log:        logger,
		localPorts: ports,
		registry:   registry.New(),
		podIP:      localPodIP(),
		nodeName:   localNodeName(),
		handles:        make(map[int]*portHandle),
		registerWarned: make(map[string]struct{}),
	}
	return srv, nil
}

// ListenAndServe accepts Unix connections until ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.cfg.SocketPath), 0o755); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}
	_ = os.Remove(s.cfg.SocketPath)
	ln, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix %q: %w", s.cfg.SocketPath, err)
	}
	defer ln.Close()
	if err := os.Chmod(s.cfg.SocketPath, 0o666); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	if s.cfg.Fabric {
		if _, err := s.startFabric(ctx); err != nil {
			return err
		}
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

type portHandle struct {
	caName string
	port   int
	recvQ  [][]byte
	mu     sync.Mutex
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer c.Close()
	for {
		if ctx.Err() != nil {
			return
		}
		var env protocol.Envelope
		if err := protocol.ReadEnvelope(c, &env); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			s.log.Printf("mock-ib: read: %v", err)
			return
		}
		if err := s.dispatch(c, env); err != nil {
			s.log.Printf("mock-ib: %s: %v", env.Type, err)
			return
		}
	}
}

func (s *Server) dispatch(c net.Conn, env protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeOpen:
		var req protocol.OpenReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleOpen(c, req)
	case protocol.TypeSend:
		var req protocol.SendReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleSend(c, req)
	case protocol.TypeRecv:
		var req protocol.RecvReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleRecv(c, req)
	case protocol.TypeClose:
		var req protocol.CloseReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleClose(c, req)
	case protocol.TypeRegisterPeers:
		s.registerWithPeers(context.Background())
		return protocol.WriteMessage(c, protocol.TypeRegisterPeers, map[string]bool{"ok": true})
	default:
		return protocol.WriteMessage(c, env.Type, map[string]string{
			"error": fmt.Sprintf("unknown message type %q", env.Type),
		})
	}
}

func (s *Server) handleOpen(c net.Conn, req protocol.OpenReq) error {
	if req.CAName == "" || req.Port <= 0 {
		return protocol.WriteMessage(c, protocol.TypeOpen, protocol.OpenResp{Error: "ca_name and port required"})
	}
	ports, err := sysfs.Scan(s.cfg.IBRoot)
	if err != nil {
		return protocol.WriteMessage(c, protocol.TypeOpen, protocol.OpenResp{Error: err.Error()})
	}
	var found bool
	for _, p := range ports {
		if p.CAName == req.CAName && p.Port == req.Port {
			found = true
			break
		}
	}
	if !found {
		return protocol.WriteMessage(c, protocol.TypeOpen, protocol.OpenResp{
			Error: fmt.Sprintf("unknown CA/port %s:%d", req.CAName, req.Port),
		})
	}
	s.handlesMu.Lock()
	for id, h := range s.handles {
		if h.caName == req.CAName && h.port == req.Port {
			s.handlesMu.Unlock()
			return protocol.WriteMessage(c, protocol.TypeOpen, protocol.OpenResp{Handle: id})
		}
	}
	id := s.nextHandleID + 1
	s.nextHandleID = id
	s.handles[id] = &portHandle{caName: req.CAName, port: req.Port}
	s.handlesMu.Unlock()
	return protocol.WriteMessage(c, protocol.TypeOpen, protocol.OpenResp{Handle: id})
}

func (s *Server) handleSend(c net.Conn, req protocol.SendReq) error {
	h, ok := s.lookupHandle(req.Handle)
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{Error: "invalid handle"})
	}
	if len(req.MAD) == 0 {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{Error: "empty mad"})
	}
	if s.trySAPathQuery(h, req.MAD) {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
	}
	if s.cfg.Fabric && s.tryFabricSend(h, req.MAD) {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
	}
	queue := s.loopback.ShouldQueueRecv(req.MAD)
	if queue {
		resp := s.loopback.SynthesizeRecv(req.MAD)
		h.mu.Lock()
		h.recvQ = append(h.recvQ, resp)
		h.mu.Unlock()
	}
	return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
}

func (s *Server) handleRecv(c net.Conn, req protocol.RecvReq) error {
	h, ok := s.lookupHandle(req.Handle)
	if ok {
		h.mu.Lock()
		h.mu.Unlock()
	}
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeRecv, protocol.RecvResp{Error: "invalid handle"})
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 1000 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		h.mu.Lock()
		if len(h.recvQ) > 0 {
			mad := h.recvQ[0]
			h.recvQ = h.recvQ[1:]
			h.mu.Unlock()
			return protocol.WriteMessage(c, protocol.TypeRecv, protocol.RecvResp{MAD: mad})
		}
		h.mu.Unlock()
		if time.Now().After(deadline) {
			return protocol.WriteMessage(c, protocol.TypeRecv, protocol.RecvResp{Timeout: true})
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *Server) handleClose(c net.Conn, req protocol.CloseReq) error {
	s.handlesMu.Lock()
	delete(s.handles, req.Handle)
	s.handlesMu.Unlock()
	return protocol.WriteMessage(c, protocol.TypeClose, protocol.CloseResp{OK: true})
}

func (s *Server) lookupHandle(id int) (*portHandle, bool) {
	s.handlesMu.Lock()
	defer s.handlesMu.Unlock()
	h, ok := s.handles[id]
	return h, ok
}
