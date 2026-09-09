// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/gid"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/protocol"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/registry"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/subnet"
)

func (s *Server) startFabric(ctx context.Context) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", s.cfg.TCPPort))

	if err != nil {
		return nil, fmt.Errorf("listen tcp :%d: %w", s.cfg.TCPPort, err)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	go s.registerWithPeersLoop(ctx)
	go s.acceptFabric(ctx, ln)

	return ln, nil
}

// registerWithPeersLoop retries REGISTER until ctx is canceled (MOCK_IB_PEERS).
func (s *Server) registerWithPeersLoop(ctx context.Context) {
	// Peer pods become reachable at different times; dialing at t=0 only logs refusals.
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	for {
		s.registerWithPeers(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Server) acceptFabric(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// A transient Accept error (e.g. EMFILE, ECONNABORTED) must not
			// kill cross-pod routing for the rest of the pod's life. Back off
			// briefly and keep serving; only a closed listener is fatal.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("fabric listener accept failed; retrying", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		go s.serveFabricConn(ctx, conn)
	}
}

func (s *Server) serveFabricConn(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(fabricConnIdleTimeout))
		var env protocol.Envelope
		if err := protocol.ReadEnvelope(c, &env); err != nil {
			// EOF (peer closed after its REGISTER/Ping) and a stalled-peer read
			// timeout are expected; only unexpected errors are logged.
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil {
				return
			}
			s.log.Warn("dropping fabric connection: read failed", zap.Error(err))
			return
		}
		// Symmetric to the read deadline above: cap the Pong/response write so
		// a peer that stops reading mid-exchange cannot pin this goroutine.
		_ = c.SetWriteDeadline(time.Now().Add(fabricConnIdleTimeout))
		if err := s.dispatchFabric(c, env); err != nil {
			s.log.Warn("dropping fabric connection: peer message rejected", zap.String("type", env.Type), zap.Error(err))
			return
		}
	}
}

func (s *Server) dispatchFabric(c net.Conn, env protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeRegister:
		var body protocol.RegisterBody
		if err := protocol.DecodeBody(env, &body); err != nil {
			return err
		}
		s.applyRegister(body)
		return nil
	case protocol.TypePing:
		var ping protocol.PingBody
		if err := protocol.DecodeBody(env, &ping); err != nil {
			return err
		}
		return s.handleFabricPing(c, ping)
	default:
		return fmt.Errorf("unknown fabric message type %q", env.Type)
	}
}

func (s *Server) applyRegister(body protocol.RegisterBody) {
	changed := make([]bool, len(body.Ports))
	for i, port := range body.Ports {
		changed[i] = s.registry.Register(port.PortGUID, registry.Peer{
			PodIP:    body.PodIP,
			NodeName: body.NodeName,
			CAName:   port.CAName,
			Port:     port.Port,
			LID:      port.LID,
		})
	}
	s.rebuildGraph()
	// Cross-pod diagnostics fail silently when a REGISTER never lands, so log
	// arrivals unconditionally — absence of this line localizes the fault to
	// the network rather than to MAD routing. Only changes are logged; peers
	// re-register every 2s and the steady-state repeats carry no information.
	for i, port := range body.Ports {
		if !changed[i] {
			continue
		}
		s.log.Info("peer registered", zap.String("pod_ip", body.PodIP), zap.String("node", body.NodeName),
			zap.String("ca", port.CAName), zap.Int("port", port.Port), zap.String("lid", lidHex(port.LID)),
			zap.String("port_guid", port.PortGUID))
	}
}

func (s *Server) handleFabricPing(c net.Conn, ping protocol.PingBody) error {
	if !s.pingTargetsLocalPort(ping) {
		// The peer's tryFabricSend resolved this MAD to *our* PodIP, so the
		// dst either has the wrong LID or the wrong port_guid for our locals.
		// Logging this here is the only way to spot a one-shot REGISTER that
		// shipped stale/wrong port advertisements without re-running the
		// validate-ibping.sh harness by hand.
		s.log.Warn("fabric ping addressed no local port", zap.String("dst_lid", lidHex(ping.DstLID)), zap.String("dst_port_guid", ping.DstPortGUID))
		return nil
	}
	return protocol.WriteMessage(c, protocol.TypePong, protocol.PongBody{
		Seq:      ping.Seq,
		ServerTS: time.Now().UnixNano(),
	})
}

func (s *Server) pingTargetsLocalPort(ping protocol.PingBody) bool {
	if ping.DstPortGUID != "" && s.hasLocalPortGUID(ping.DstPortGUID) {
		return true
	}
	if ping.DstLID != 0 {
		for _, p := range s.localPorts {
			if p.LID == ping.DstLID {
				return true
			}
		}
	}
	return false
}

//nolint:cyclop // existing complexity; refactor deferred
func (s *Server) registerWithPeers(ctx context.Context) {
	peers := parsePeerList(envOr(envMockIBPeers, ""))

	if len(peers) == 0 {
		peers = discoverPeerIPs(ctx, envOr(envMockIBPingServiceHost, ""), s.podIP)
	}

	if len(peers) == 0 {
		return
	}

	body := protocol.RegisterBody{
		NodeName: s.nodeName,
		PodIP:    s.podIP,
		Ports:    s.localPorts,
	}

	wantPeers := 0
	var ok int

	for _, peerIP := range peers {
		// Sequential sweep with up-to-5s dials per peer: once ctx is canceled
		// (SIGTERM), stop between peers instead of walking the rest of the list.
		if ctx.Err() != nil {
			return
		}
		if peerIP == s.podIP {
			continue
		}
		wantPeers++
		if err := s.sendRegister(peerIP, body); err != nil {
			if ctx.Err() == nil {
				s.logRegisterError(peerIP, err)
			}
			continue
		}
		s.clearRegisterWarn(peerIP)
		ok++
	}

	if ctx.Err() != nil || wantPeers == 0 {
		return
	}

	// Concurrent callers may both log the same step (harmless); the atomic
	// only guarantees the read-modify-write is race-free, not single-shot.
	if int32(ok) > s.lastPeerRegisterOK.Load() {
		switch {
		case ok >= wantPeers:
			s.log.Info("fabric ready", zap.Int("ports", len(body.Ports)), zap.Int("peers", wantPeers))
		default:
			s.log.Info("fabric converging", zap.Int("ports", len(body.Ports)), zap.Int("registered_peers", ok), zap.Int("peers", wantPeers))
		}
		s.lastPeerRegisterOK.Store(int32(ok))
	}
}

func (s *Server) logRegisterError(peerIP string, err error) {
	if isPeerNotReady(err) {
		s.registerWarnedMu.Lock()
		_, seen := s.registerWarned[peerIP]
		if !seen {
			s.registerWarned[peerIP] = struct{}{}
			s.log.Info("peer not listening yet; retrying every 2s", zap.String("peer_ip", peerIP))
		}
		s.registerWarnedMu.Unlock()
		return
	}
	s.log.Warn("peer registration failed; will retry", zap.String("peer_ip", peerIP), zap.Error(err))
}

func (s *Server) clearRegisterWarn(peerIP string) {
	s.registerWarnedMu.Lock()
	delete(s.registerWarned, peerIP)
	s.registerWarnedMu.Unlock()
}

func isPeerNotReady(err error) bool {
	if err == nil {
		return false
	}

	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}

	return strings.Contains(err.Error(), "connection refused")
}

func (s *Server) sendRegister(peerIP string, body protocol.RegisterBody) error {
	addr := net.JoinHostPort(peerIP, strconv.Itoa(s.cfg.TCPPort))
	conn, err := net.DialTimeout("tcp", addr, fabricPeerIOTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return writeRegister(conn, fabricPeerIOTimeout, body)
}

// writeRegister bounds the REGISTER write so a peer that accepts but never
// reads cannot wedge the sequential registerWithPeersLoop forever (mirrors
// pingPeer). Split from sendRegister so the deadline path is testable against
// an unbuffered net.Pipe instead of kernel-buffered TCP sockets.
func writeRegister(conn net.Conn, timeout time.Duration, body protocol.RegisterBody) error {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	return protocol.WriteMessage(conn, protocol.TypeRegister, body)
}

func (s *Server) pingPeer(peerIP, portGUID string, dstLID uint16) error {
	addr := net.JoinHostPort(peerIP, strconv.Itoa(s.cfg.TCPPort))
	conn, err := net.DialTimeout("tcp", addr, fabricPeerIOTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(fabricPeerIOTimeout)); err != nil {
		return err
	}

	seq := atomic.AddUint32(&s.nextSeq, 1)
	ping := protocol.PingBody{
		DstPortGUID: portGUID,
		DstLID:      dstLID,
		Seq:         seq,
		ClientTS:    time.Now().UnixNano(),
	}
	if err := protocol.WriteMessage(conn, protocol.TypePing, ping); err != nil {
		return err
	}
	var env protocol.Envelope
	if err := protocol.ReadEnvelope(conn, &env); err != nil {
		return err
	}
	if env.Type != protocol.TypePong {
		return fmt.Errorf("unexpected fabric response %q", env.Type)
	}
	var pong protocol.PongBody
	if err := protocol.DecodeBody(env, &pong); err != nil {
		return err
	}
	if pong.Seq != seq {
		return fmt.Errorf("pong seq mismatch: got %d want %d", pong.Seq, seq)
	}
	return nil
}

func (s *Server) hasLocalPortGUID(guid string) bool {
	key := gid.NormalizePortGUID(guid)

	for _, p := range s.localPorts {
		if gid.NormalizePortGUID(p.PortGUID) == key {
			return true
		}
	}

	return false
}

//nolint:cyclop // existing complexity; refactor deferred
func (s *Server) tryFabricSend(h *portHandle, sendMad []byte) bool {
	// Redundant with handleSend's gate, deliberately: routing a subnet
	// management packet over the fabric answers an iblinkinfo SMP with a ping
	// reply, which breaks link discovery without erroring anywhere.
	if subnet.IsSMPSend(sendMad) {
		return false
	}
	if s.loopback.matchesLocal(sendMad) {
		return false
	}
	var (
		peer  registry.Peer
		guid  string
		found bool
	)
	if g, ok := destPortGUID(sendMad); ok {
		peer, found = s.registry.Lookup(g)
		guid = g
	}
	if !found {
		if lid, ok := destLID(sendMad); ok {
			peer, guid, found = s.registry.LookupByLID(lid)
		}
	}
	if !found || peer.PodIP == "" || peer.PodIP == s.podIP {
		// Useful diagnostic for cross-pod ibping failures: print the
		// destination we could not route AND the registry size so it is
		// immediately obvious whether REGISTER never arrived (size=0) or
		// arrived with the wrong key (size>0 + miss).
		if dstLID, ok := destLID(sendMad); ok && dstLID != 0 {
			s.log.Warn("no fabric route to destination LID", zap.String("dst_lid", lidHex(dstLID)), zap.Int("registry_size", s.registry.Size()),
				zap.String("self_pod_ip", s.podIP), zap.String("peer_pod_ip", peer.PodIP))
		}
		return false
	}
	var dstLID uint16
	if lid, ok := destLID(sendMad); ok {
		dstLID = lid
	} else {
		dstLID = peer.LID
	}
	if err := s.pingPeer(peer.PodIP, guid, dstLID); err != nil {
		s.log.Warn("fabric ping to peer failed", zap.String("port_guid", guid), zap.String("peer_ip", peer.PodIP), zap.Error(err))
		return false
	}
	resp := s.loopback.SynthesizeRecv(sendMad)
	h.mu.Lock()
	h.recvQ = append(h.recvQ, resp)
	h.mu.Unlock()
	return true
}

func localPodIP() string {
	for _, key := range []string{"POD_IP", "MOCK_IB_POD_IP"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return "127.0.0.1"
}

func localNodeName() string {
	if v := os.Getenv("NODE_NAME"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}
