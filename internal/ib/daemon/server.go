// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package daemon serves the InfiniBand control plane the LD_PRELOAD shims talk
// to: UMAD and verbs over a Unix socket, plus a TCP relay that exchanges port
// registrations with peer pods so diagnostics can see a multi-node fabric.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/fabric"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/protocol"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/registry"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/subnet"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
)

// Config holds mock-ib runtime options.
type Config struct {
	SocketPath string
	IBRoot     string
	TCPPort    int
	Fabric     bool
}

// Every deadline here bounds a goroutine. The Unix socket is 0666 and the
// fabric listener is unauthenticated, so a client that connects and then stalls
// must not pin a goroutine for the pod's lifetime.
const (
	// Diag tools finish in seconds, with sub-second gaps between frames.
	unixConnIdleTimeout = 5 * time.Minute
	// A fabric exchange is one REGISTER or Ping/Pong round-trip, so a peer that
	// connects without completing a frame is reaped far sooner.
	fabricConnIdleTimeout = 30 * time.Second
	// Covers dial plus exchange on outbound connections. registerWithPeersLoop
	// walks peers sequentially, so one dead peer must not stall the sweep.
	fabricPeerIOTimeout = 5 * time.Second
	// The client supplies recv timeout_ms over the world-writable socket, so it
	// is capped rather than trusted.
	recvTimeoutCap = 60 * time.Second

	defaultRecvTimeout = time.Second
	recvPollInterval   = 5 * time.Millisecond
)

// Server serves libibmockumad over a Unix socket.
type Server struct {
	cfg      Config
	loopback *loopbackIndex
	log      *slog.Logger

	localPorts []protocol.PortAdvert
	registry   *registry.Registry
	podIP      string
	nodeName   string
	nextSeq    uint32

	nextHandleID int
	handles      map[int]*portHandle
	handlesMu    sync.Mutex

	nextVerbsHandleID int
	verbsHandles      map[int]*verbsHandle
	verbsMu           sync.RWMutex

	graphMu sync.RWMutex
	graph   *fabric.Graph

	// Peers boot at different times, so the first registration sweeps fail with
	// "connection refused". Tracking the last outcome keeps that transient out
	// of the log until it stops being transient.
	lastPeerRegisterOK atomic.Int32
	registerWarned     map[string]struct{}
	registerWarnedMu   sync.Mutex
}

// NewServer builds a server; local ports are loaded from ib-root sysfs.
func NewServer(cfg Config) (*Server, error) {
	ports, err := sysfs.Scan(cfg.IBRoot)
	if err != nil {
		return nil, fmt.Errorf("scan ib-root %q: %w", cfg.IBRoot, err)
	}
	srv := &Server{
		cfg:            cfg,
		log:            newLogger(),
		loopback:       newLoopbackIndex(ports),
		localPorts:     ports,
		registry:       registry.New(),
		podIP:          localPodIP(),
		nodeName:       localNodeName(),
		handles:        make(map[int]*portHandle),
		verbsHandles:   make(map[int]*verbsHandle),
		registerWarned: make(map[string]struct{}),
	}
	srv.rebuildGraph()
	return srv, nil
}

func (s *Server) rebuildGraph() {
	g := fabric.Build(s.localPorts, s.registry.Snapshot())
	s.graphMu.Lock()
	s.graph = g
	s.graphMu.Unlock()
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
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(s.cfg.SocketPath, 0o666); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	if s.cfg.Fabric {
		if _, err := s.startFabric(ctx); err != nil {
			return err
		}
	}

	s.logStartup()

	go func() {
		<-ctx.Done()
		s.log.Info("shutting down", "socket", s.cfg.SocketPath)
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

// logStartup announces the listener and its port inventory. Emitted
// unconditionally: cross-pod ibping cannot work without this listener, so the
// absence of these lines on a peer pod localizes a failure to startup rather
// than to MAD routing.
func (s *Server) logStartup() {
	s.log.Info("serving", "socket", s.cfg.SocketPath, "fabric", s.cfg.Fabric, "port", s.cfg.TCPPort,
		"pod_ip", s.podIP, "node", s.nodeName, "local_ports", len(s.localPorts))

	if len(s.localPorts) == 0 {
		// The commonest misconfiguration, and it surfaces downstream as
		// "0 HCAs found" with nothing pointing back here.
		slog.Warn("no local ports found; ibstat and ibping will report nothing",
			"ib_root", s.cfg.IBRoot)
		return
	}

	for _, p := range s.localPorts {
		s.log.Info("local port", "ca", p.CAName, "port", p.Port, "lid", lidHex(p.LID), "port_guid", p.PortGUID)
	}
}

type portHandle struct {
	caName string
	port   int
	recvQ  [][]byte
	mu     sync.Mutex
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(unixConnIdleTimeout))
		var env protocol.Envelope
		if err := protocol.ReadEnvelope(c, &env); err != nil {
			// EOF (clean close) and a stalled-client read timeout are both
			// expected lifecycle events, not errors worth logging.
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil {
				return
			}
			s.log.Warn("dropping client connection: read failed", "err", err)
			return
		}
		// Bound the response write the same way the read above is bounded: a
		// client that sends a request frame then stops reading must not pin
		// this goroutine for the pod's life on a full socket buffer.
		_ = c.SetWriteDeadline(time.Now().Add(unixConnIdleTimeout))
		if err := s.dispatch(ctx, c, env); err != nil {
			s.log.Warn("dropping client connection: request failed", "type", env.Type, "err", err)
			return
		}
	}
}

//nolint:cyclop // existing complexity; refactor deferred
func (s *Server) dispatch(ctx context.Context, c net.Conn, env protocol.Envelope) error {
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
		return s.handleRecv(ctx, c, req)
	case protocol.TypeClose:
		var req protocol.CloseReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleClose(c, req)
	case protocol.TypeVerbsOpen, protocol.TypeVerbsWrite, protocol.TypeVerbsRead, protocol.TypeVerbsClose:
		return s.dispatchVerbs(c, env)
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
	var found bool
	for _, p := range s.localPorts {
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

//nolint:cyclop // existing complexity; refactor deferred
func (s *Server) handleSend(c net.Conn, req protocol.SendReq) error {
	h, ok := s.lookupHandle(req.Handle)
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{Error: "invalid handle"})
	}
	if len(req.MAD) == 0 {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{Error: "empty mad"})
	}
	// Reject a truncated umad buffer before any downstream wire-offset read.
	// The real shim always sends a full 56-byte umad header + MAD, but a short
	// buffer sent over the local 0666 Unix socket would otherwise slice
	// umad[umadMADOffset:] out of range (e.g. in the SA path query) and panic
	// the serveConn goroutine, taking the daemon down.
	if len(req.MAD) < umadMADOffset+ibMADCommonHdrLen {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{Error: "mad too short"})
	}
	s.graphMu.RLock()
	g := s.graph
	s.graphMu.RUnlock()
	if resp, ok := subnet.TrySynthesize(req.MAD, g, h.caName); ok {
		h.mu.Lock()
		h.recvQ = append(h.recvQ, resp)
		h.mu.Unlock()
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
	}
	if s.trySAPathQuery(h, req.MAD) {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
	}
	// Fabric ping + loopback echo is for vendor/ping MADs only; SMP must use subnet synthesis.
	if s.cfg.Fabric && !subnet.IsSMPSend(req.MAD) && s.tryFabricSend(h, req.MAD) {
		return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
	}
	// Subnet SMP must not fall through to loopback echo (empty payload breaks iblinkinfo).
	if !subnet.IsSMPSend(req.MAD) {
		queue := s.loopback.ShouldQueueRecv(req.MAD)
		if queue {
			resp := s.loopback.SynthesizeRecv(req.MAD)
			h.mu.Lock()
			h.recvQ = append(h.recvQ, resp)
			h.mu.Unlock()
		}
	}
	return protocol.WriteMessage(c, protocol.TypeSend, protocol.SendResp{OK: true})
}

func (s *Server) handleRecv(ctx context.Context, c net.Conn, req protocol.RecvReq) error {
	h, ok := s.lookupHandle(req.Handle)
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeRecv, protocol.RecvResp{Error: "invalid handle"})
	}
	deadline := time.Now().Add(effectiveRecvTimeout(req.TimeoutMS))
	ticker := time.NewTicker(recvPollInterval)
	defer ticker.Stop()
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
		select {
		case <-ctx.Done():
			// Daemon shutdown: complete the in-flight recv as a Timeout — the
			// client's umad_recv maps it to EWOULDBLOCK, its normal no-data
			// path — instead of dropping the RPC mid-frame. serveConn then
			// exits on its next ctx check and closes the connection, matching
			// how shutdown is treated as an expected event elsewhere.
			return protocol.WriteMessage(c, protocol.TypeRecv, protocol.RecvResp{Timeout: true})
		case <-ticker.C:
		}
	}
}

// effectiveRecvTimeout converts the client-supplied recv timeout_ms into a
// bounded wait: timeoutMS <= 0 selects the default, and anything above
// recvTimeoutCap (including values that would overflow a time.Duration) is
// clamped to the cap so an untrusted client cannot pick the poll duration.
func effectiveRecvTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return defaultRecvTimeout
	}
	if timeoutMS >= int(recvTimeoutCap/time.Millisecond) {
		return recvTimeoutCap
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func (s *Server) handleClose(c net.Conn, req protocol.CloseReq) error {
	s.handlesMu.Lock()
	_, ok := s.handles[req.Handle]
	if ok {
		delete(s.handles, req.Handle)
	}
	s.handlesMu.Unlock()
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeClose, protocol.CloseResp{Error: "invalid handle"})
	}
	return protocol.WriteMessage(c, protocol.TypeClose, protocol.CloseResp{OK: true})
}

func (s *Server) lookupHandle(id int) (*portHandle, bool) {
	s.handlesMu.Lock()
	defer s.handlesMu.Unlock()
	h, ok := s.handles[id]
	return h, ok
}
